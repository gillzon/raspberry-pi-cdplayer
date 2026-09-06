package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type readCall struct{ sector, count int }
type fakeReader struct {
	ctx    context.Context
	calls  chan readCall
	allow  chan struct{}
	closed atomic.Bool
	err    error
}

func (r *fakeReader) Read(sector, count int) ([]byte, error) {
	select {
	case r.calls <- readCall{sector, count}:
	case <-r.ctx.Done():
		return nil, r.ctx.Err()
	}
	select {
	case <-r.allow:
	case <-r.ctx.Done():
		return nil, r.ctx.Err()
	}
	if r.err != nil {
		return nil, r.err
	}
	return bytes.Repeat([]byte{byte(sector % 251)}, count*sectorBytes), nil
}
func (r *fakeReader) Close() error { r.closed.Store(true); return nil }
func nextRead(t *testing.T, r *fakeReader) readCall {
	t.Helper()
	select {
	case v := <-r.calls:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not run")
		return readCall{}
	}
}
func setupCache(t *testing.T) (*Cache, *fakeReader, *session) {
	t.Helper()
	r := &fakeReader{calls: make(chan readCall, 16), allow: make(chan struct{}, 16)}
	c := &Cache{Root: t.TempDir(), BaseURL: "http://localhost", Open: func(ctx context.Context, _ string, _ []Layout) (Reader, error) { r.ctx = ctx; return r, nil }}
	if err := c.Observe("disc", "/dev/fake", []Layout{{1, 0, 300}, {3, 300, 600}}); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	t.Cleanup(func() {
		c.Close()
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
			t.Error("cache worker did not stop")
		}
	})
	return c, r, s
}
func TestSelectedTrackPrioritizedAndCachedBytesServed(t *testing.T) {
	c, r, _ := setupCache(t)
	if v := nextRead(t, r); v.sector != 0 {
		t.Fatal(v)
	}
	c.Select(3)
	r.allow <- struct{}{}
	if v := nextRead(t, r); v.sector != 300 {
		t.Fatalf("new selection not prioritized: %+v", v)
	}
	r.allow <- struct{}{}
	nextRead(t, r) // next block starts only after first selected block is committed
	request := httptest.NewRequest("GET", c.TrackURL(3), nil)
	request.Header.Set("Range", "bytes=44-47")
	response := httptest.NewRecorder()
	c.ServeHTTP(response, request)
	if response.Code != 206 || !bytes.Equal(response.Body.Bytes(), bytes.Repeat([]byte{byte(300 % 251)}, 4)) {
		t.Fatalf("range: %d %v", response.Code, response.Body.Bytes())
	}
	if status := c.Status(); status.CachedBytes != 2*blockSectors*sectorBytes {
		t.Fatalf("cache stats: %+v", status)
	}
	// Header is immediately available; it advertises the full eventual track.
	request = httptest.NewRequest("GET", c.TrackURL(3), nil)
	request.Header.Set("Range", "bytes=0-43")
	response = httptest.NewRecorder()
	c.ServeHTTP(response, request)
	if response.Code != 206 || string(response.Body.Bytes()[:4]) != "RIFF" || binary.LittleEndian.Uint32(response.Body.Bytes()[40:]) != 300*sectorBytes {
		t.Fatal("incorrect WAV header")
	}
}
func TestEjectInvalidatesURLsAndCancelsWaitingAudio(t *testing.T) {
	c, r, s := setupCache(t)
	nextRead(t, r)
	url := c.TrackURL(1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		request := httptest.NewRequest("GET", url, nil)
		request.Header.Set("Range", "bytes=44-47")
		c.ServeHTTP(httptest.NewRecorder(), request)
	}()
	c.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("audio request survived disc removal")
	}
	<-s.done
	if !r.closed.Load() {
		t.Fatal("reader not closed")
	}
	response := httptest.NewRecorder()
	c.ServeHTTP(response, httptest.NewRequest("GET", url, nil))
	if response.Code != 404 {
		t.Fatal("old disc URL still accessible")
	}
}
func TestMissingBlocksNeverReadSparseFileZeros(t *testing.T) {
	c, r, s := setupCache(t)
	nextRead(t, r)
	reader := &cachedReader{ctx: context.Background(), session: s, track: s.tracks[0], offset: 44}
	done := make(chan []byte, 1)
	go func() {
		b := make([]byte, 4)
		_, err := reader.Read(b)
		if err != nil {
			done <- nil
		} else {
			done <- b
		}
	}()
	select {
	case <-done:
		t.Fatal("uncached block returned before read")
	case <-time.After(20 * time.Millisecond):
	}
	r.allow <- struct{}{}
	select {
	case b := <-done:
		if len(b) != 4 {
			t.Fatal("read failed")
		}
	case <-time.After(time.Second):
		t.Fatal("cached bytes did not unblock reader")
	}
	_ = c
}
func TestReaderErrorPropagatesWithoutInventedAudio(t *testing.T) {
	c := &Cache{Root: t.TempDir(), Open: func(context.Context, string, []Layout) (Reader, error) { return nil, io.ErrUnexpectedEOF }}
	if err := c.Observe("disc", "/dev/fake", []Layout{{1, 0, 75}}); err != nil {
		t.Fatal(err)
	}
	s := c.session
	defer func() { c.Close(); <-s.done }()
	reader := &cachedReader{ctx: context.Background(), session: s, track: s.tracks[0], offset: 44}
	if _, err := reader.Read(make([]byte, 4)); err == nil {
		t.Fatal("missing read error")
	}
	if !strings.Contains(c.Status().Error, "unexpected EOF") {
		t.Fatal("missing error status")
	}
}

func TestReadyWaitsForAudioAndReportsFailure(t *testing.T) {
	c, r, s := setupCache(t)
	nextRead(t, r)
	if c.Ready() != ErrPreparing {
		t.Fatal("ready before audio exists")
	}
	r.allow <- struct{}{}
	nextRead(t, r)
	if err := c.Ready(); err != nil {
		t.Fatalf("audio not ready: %v", err)
	}
	failure := fmt.Errorf("track 13 layout mismatch: expected 100..200, reader reported 100..150")
	s.fail(failure)
	if c.Ready() != failure {
		t.Fatal("reader failure hidden")
	}
}

func TestAdjacentStartsCachedBeforeRestOfCurrentTrack(t *testing.T) {
	r := &fakeReader{calls: make(chan readCall, 1), allow: make(chan struct{}, 1)}
	c := &Cache{Root: t.TempDir(), Open: func(ctx context.Context, _ string, _ []Layout) (Reader, error) { r.ctx = ctx; return r, nil }}
	if err := c.Observe("long", "/dev/fake", []Layout{{1, 0, 9000}, {3, 9000, 18000}, {7, 18000, 27000}}); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	check := func(sector int) {
		t.Helper()
		if got := nextRead(t, r); got.sector != sector {
			t.Fatalf("read %d, want %d", got.sector, sector)
		}
		r.allow <- struct{}{}
	}
	// Current track gets a cushion, then Next gets its intro before the
	// remaining 90 seconds of the current track are read.
	for n := 0; n < 30; n++ {
		check(n * 75)
	}
	for n := 0; n < 10; n++ {
		check(9000 + n*75)
	}
	if got := nextRead(t, r); got.sector != 30*75 {
		t.Fatalf("did not resume current track: %+v", got)
	}
	// Jump to the last track while a read is in flight. Its first blocks
	// take priority, then Previous is primed even though it is not selected.
	c.Select(7)
	r.allow <- struct{}{}
	for n := 0; n < 30; n++ {
		check(18000 + n*75)
	}
	// Previous was already primed, so no duplicate reads are needed.
	if got := nextRead(t, r); got.sector != 18000+30*75 {
		t.Fatalf("cached previous intro reread: %+v", got)
	}
	// Demand on the selected stream must preempt further speculative reads.
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	s.mu.Lock()
	s.demand[s.tracks[2]] = 80
	s.mu.Unlock()
	r.allow <- struct{}{}
	if got := nextRead(t, r); got.sector != 18000+80*75 {
		t.Fatalf("live demand not prioritized: %+v", got)
	}
}
