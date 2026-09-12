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

func TestContinuousTrackReadAndManualSelection(t *testing.T) {
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
	// Read all 120 seconds sequentially, including past the former 30s
	// cutoff. No adjacent-track seek may interrupt continuous playback.
	for n := 0; n < 9000/blockSectors; n++ {
		check(n * blockSectors)
	}
	if got := nextRead(t, r); got.sector != 9000 {
		t.Fatalf("next track not cached after current: %+v", got)
	}
	// A manual selection still takes priority at the next read boundary.
	c.Select(7)
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	s.mu.Lock()
	s.demand[s.tracks[1]] = 20
	s.mu.Unlock()
	r.allow <- struct{}{}
	// A pending request from the old stream must not cause a seek back
	// after priming the new track's first two blocks.
	for n := 0; n < 40; n++ {
		check(18000 + n*blockSectors)
	}
	if got := nextRead(t, r); got.sector != 18000+40*blockSectors {
		t.Fatalf("new track interrupted: %+v", got)
	}
	s.mu.Lock()
	s.demand[s.tracks[2]] = 80
	s.mu.Unlock()
	r.allow <- struct{}{}
	if got := nextRead(t, r); got.sector != 18000+80*blockSectors {
		t.Fatalf("selected stream demand ignored: %+v", got)
	}
}

func TestIntentionalCancellationIsNotCacheFailure(t *testing.T) {
	_, r, s := setupCache(t)
	nextRead(t, r)
	s.cancel()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		t.Fatalf("cancellation reported as failure: %v", s.err)
	}
}

func TestFirstAudioWakesPlayerOnlyAfterDataIsReady(t *testing.T) {
	wake := make(chan struct{}, 1)
	r := &fakeReader{calls: make(chan readCall, 16), allow: make(chan struct{}, 16)}
	c := &Cache{Root: t.TempDir(), ReadyNotify: wake, Open: func(ctx context.Context, _ string, _ []Layout) (Reader, error) { r.ctx = ctx; return r, nil }}
	if err := c.Observe("disc", "/dev/fake", []Layout{{1, 0, 300}}); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	if got := nextRead(t, r); got.sector != 0 || got.count > 15 {
		t.Fatalf("startup waits for more than 200ms of audio: %+v", got)
	}
	select {
	case <-wake:
		t.Fatal("notified before audio read")
	default:
	}
	if c.Ready() != ErrPreparing {
		t.Fatal("ready without audio")
	}
	r.allow <- struct{}{}
	select {
	case <-wake:
	case <-time.After(2 * time.Second):
		t.Fatal("missing readiness notification")
	}
	if err := c.Ready(); err != nil {
		t.Fatal(err)
	}
	nextRead(t, r)
	r.allow <- struct{}{}
	nextRead(t, r)
	select {
	case <-wake:
		t.Fatal("repeated startup notification")
	default:
	}
}

func TestShortFinalBlockServesExactTrackLength(t *testing.T) {
	r := &fakeReader{calls: make(chan readCall, 2), allow: make(chan struct{}, 2)}
	c := &Cache{Root: t.TempDir(), BaseURL: "http://localhost", Open: func(ctx context.Context, _ string, _ []Layout) (Reader, error) { r.ctx = ctx; return r, nil }}
	if err := c.Observe("short", "/dev/fake", []Layout{{1, 0, 17}}); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	for _, want := range []readCall{{0, 15}, {15, 2}} {
		if got := nextRead(t, r); got != want {
			t.Fatalf("read %+v, want %+v", got, want)
		}
		r.allow <- struct{}{}
	}
	response := httptest.NewRecorder()
	c.ServeHTTP(response, httptest.NewRequest("GET", c.TrackURL(1), nil))
	want := append(wavHeader(17*sectorBytes), make([]byte, 15*sectorBytes)...)
	want = append(want, bytes.Repeat([]byte{15}, 2*sectorBytes)...)
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), want) {
		t.Fatalf("incomplete or padded audio: status=%d bytes=%d", response.Code, response.Body.Len())
	}
}
