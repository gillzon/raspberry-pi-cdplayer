package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/driveio"
)

type recoveryReader struct {
	read  func(int, int) ([]byte, error)
	close func()
}

func TestRecoveryPreservesShortReadsAndResumesAtMissingSector(t *testing.T) {
	for _, withError := range []bool{false, true} {
		t.Run(fmt.Sprintf("data_with_error=%v", withError), func(t *testing.T) {
			reopening, resume := make(chan struct{}), make(chan struct{})
			calls := make(chan readCall, 4)
			c := &Cache{Root: t.TempDir(), BaseURL: "http://localhost"}
			c.Open = func(context.Context, string, []Layout) (Reader, error) {
				return recoveryReader{read: func(start, count int) ([]byte, error) {
					if start == 0 {
						data, _ := pcmBlock(0, 3)
						if withError {
							return data, io.ErrUnexpectedEOF
						}
						return data, nil
					}
					if start != 3 || count != 12 {
						t.Errorf("short read was repeated: %d %d", start, count)
					}
					return nil, io.ErrUnexpectedEOF
				}}, nil
			}
			c.RecoveryOpen = func(ctx context.Context, _ string, _ []Layout) (Reader, error) {
				close(reopening)
				select {
				case <-resume:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return recoveryReader{read: func(start, count int) ([]byte, error) {
					calls <- readCall{start, count}
					return pcmBlock(start, count)
				}}, nil
			}
			if err := c.Observe("disc", "fake", []Layout{{1, 0, 17}}); err != nil {
				t.Fatal(err)
			}
			defer c.Shutdown()
			select {
			case <-reopening:
			case <-time.After(time.Second):
				t.Fatal("no recovery")
			}
			url := c.TrackURL(1)
			if got := c.Status(); got.CachedBytes != 3*sectorBytes || !got.Recovering || c.Ready() != nil {
				t.Fatalf("valid sectors lost during recovery: %+v", got)
			}
			r := &cachedReader{ctx: context.Background(), session: c.session, track: c.session.tracks[0], offset: 44}
			buffer := make([]byte, 15*sectorBytes)
			n, err := r.Read(buffer)
			if err != nil || n != 3*sectorBytes || !bytes.Equal(buffer[:n], bytes.Repeat([]byte{1}, n)) {
				t.Fatalf("short cached read crossed into missing sectors: n=%d err=%v", n, err)
			}
			stream, done := httptest.NewRecorder(), make(chan struct{})
			go func() { defer close(done); c.ServeHTTP(stream, httptest.NewRequest("GET", url, nil)) }()
			select {
			case <-done:
				t.Fatal("stream ended at recoverable gap")
			case <-time.After(20 * time.Millisecond):
			}
			close(resume)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("stream never recovered")
			}
			for _, want := range []readCall{{3, 12}, {15, 2}} {
				select {
				case got := <-calls:
					if got != want {
						t.Fatalf("recovery read %+v, want %+v", got, want)
					}
				case <-time.After(time.Second):
					t.Fatal("missing recovery read")
				}
			}
			want := wavHeader(17 * sectorBytes)
			for _, part := range []readCall{{0, 3}, {3, 12}, {15, 2}} {
				data, _ := pcmBlock(part.sector, part.count)
				want = append(want, data...)
			}
			if stream.Code != 200 || !bytes.Equal(stream.Body.Bytes(), want) || c.TrackURL(1) != url || c.Status().CachedBytes != 17*sectorBytes {
				t.Fatal("recovery lost or duplicated the good prefix")
			}
		})
	}
}

func TestExhaustedRecoveryKeepsPartialBlockForManualRetry(t *testing.T) {
	var opens atomic.Int32
	c := &Cache{Root: t.TempDir(), BaseURL: "http://localhost"}
	c.Open = func(context.Context, string, []Layout) (Reader, error) {
		attempt := opens.Add(1)
		return recoveryReader{read: func(start, count int) ([]byte, error) {
			if start == 0 {
				if attempt != 1 {
					t.Error("manual retry reread already cached audio")
				}
				return pcmBlock(0, 3)
			}
			if attempt <= 3 {
				return nil, io.ErrUnexpectedEOF
			}
			if start != 3 || count != 12 {
				t.Errorf("wrong retry range %d %d", start, count)
			}
			return pcmBlock(start, count)
		}}, nil
	}
	if err := c.Observe("disc", "fake", []Layout{{1, 0, 15}}); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	status := waitCache(t, c, func(s Status) bool { return s.Error != "" })
	if status.CachedBytes != 3*sectorBytes || c.Ready() != nil {
		t.Fatalf("exhaustion hid good audio: %+v", status)
	}
	url := c.TrackURL(1)
	request := httptest.NewRequest("GET", url, nil)
	request.Header.Set("Range", fmt.Sprintf("bytes=44-%d", 44+3*sectorBytes-1))
	response := httptest.NewRecorder()
	c.ServeHTTP(response, request)
	if response.Code != 206 || !bytes.Equal(response.Body.Bytes(), bytes.Repeat([]byte{1}, 3*sectorBytes)) {
		t.Fatal("exhaustion discarded cached prefix")
	}
	r := &cachedReader{ctx: context.Background(), session: c.session, track: c.session.tracks[0], offset: 44 + 3*sectorBytes}
	if _, err := r.Read(make([]byte, 4)); err == nil {
		t.Fatal("gap was replaced with silence")
	}
	if !c.Retry() {
		t.Fatal("manual retry failed")
	}
	waitCache(t, c, func(s Status) bool { return s.CachedBytes == s.TotalBytes })
	if c.TrackURL(1) != url || opens.Load() != 4 {
		t.Fatal("manual retry rebuilt cache")
	}
}

func (r recoveryReader) Read(start, count int) ([]byte, error) { return r.read(start, count) }
func (r recoveryReader) Close() error {
	if r.close != nil {
		r.close()
	}
	return nil
}

func waitCache(t *testing.T, c *Cache, condition func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := c.Status(); condition(s) {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("cache did not reach expected state: %+v", c.Status())
	return Status{}
}

func pcmBlock(start, count int) ([]byte, error) {
	return bytes.Repeat([]byte{byte(start + 1)}, count*sectorBytes), nil
}

func TestReadRecoveryKeepsHTTPStreamAndCachedBlocks(t *testing.T) {
	allowRecovery, reopening := make(chan struct{}), make(chan struct{})
	var closed, validated atomic.Bool
	c := &Cache{Root: t.TempDir(), BaseURL: "http://localhost", Access: driveio.New()}
	c.Open = func(context.Context, string, []Layout) (Reader, error) {
		return recoveryReader{read: func(start, count int) ([]byte, error) {
			if start == 0 {
				return pcmBlock(start, count)
			}
			return nil, io.ErrUnexpectedEOF
		}, close: func() { closed.Store(true) }}, nil
	}
	c.ValidateRecovery = func(device, id string) error {
		if !closed.Load() || device != "fake" || id != "disc" {
			t.Error("recovery did not validate after helper exit")
		}
		validated.Store(true)
		return nil
	}
	c.RecoveryOpen = func(ctx context.Context, _ string, _ []Layout) (Reader, error) {
		if !validated.Load() {
			t.Error("opened before disc validation")
		}
		close(reopening)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-allowRecovery:
		}
		return recoveryReader{read: pcmBlock}, nil
	}
	if err := c.Observe("disc", "fake", []Layout{{1, 0, 45}}); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	select {
	case <-reopening:
	case <-time.After(time.Second):
		t.Fatal("no recovery")
	}
	url := c.TrackURL(1)
	if status := c.Status(); !status.Recovering || status.Error != "" || status.CachedBytes != 15*sectorBytes || c.Ready() != nil {
		t.Fatalf("recovery made available audio unusable: %+v", status)
	}
	request := httptest.NewRequest("GET", url, nil)
	request.Header.Set("Range", "bytes=44-47")
	response := httptest.NewRecorder()
	c.ServeHTTP(response, request)
	if response.Code != 206 || !bytes.Equal(response.Body.Bytes(), bytes.Repeat([]byte{1}, 4)) {
		t.Fatal("cached range unavailable during recovery")
	}
	stream := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); c.ServeHTTP(stream, httptest.NewRequest("GET", url, nil)) }()
	select {
	case <-done:
		t.Fatal("HTTP stream ended instead of waiting for recovery")
	case <-time.After(20 * time.Millisecond):
	}
	close(allowRecovery)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not recover")
	}
	want := wavHeader(45 * sectorBytes)
	for _, start := range []int{0, 15, 30} {
		block, _ := pcmBlock(start, 15)
		want = append(want, block...)
	}
	if stream.Code != 200 || !bytes.Equal(stream.Body.Bytes(), want) {
		t.Fatal("recovery lost, duplicated or fabricated audio")
	}
	if c.TrackURL(1) != url || c.Status().Recovering || c.Status().RecoveryAttempts != 1 {
		t.Fatalf("unexpected recovered state: %+v", c.Status())
	}
}

func TestRecoveryBudgetAndManualRetryRetainCache(t *testing.T) {
	var opens atomic.Int32
	c := &Cache{Root: t.TempDir(), BaseURL: "http://localhost"}
	c.Open = func(context.Context, string, []Layout) (Reader, error) {
		n := opens.Add(1)
		return recoveryReader{read: func(start, count int) ([]byte, error) {
			if start == 0 || n > 3 {
				return pcmBlock(start, count)
			}
			return nil, io.ErrUnexpectedEOF
		}}, nil
	}
	if err := c.Observe("disc", "fake", []Layout{{1, 0, 30}}); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	status := waitCache(t, c, func(s Status) bool { return s.Error != "" })
	if opens.Load() != 3 || status.RecoveryAttempts != 2 || c.Ready() != nil {
		t.Fatalf("bad recovery limit: %+v opens=%d", status, opens.Load())
	}
	url := c.TrackURL(1)
	// Available blocks remain playable; an unavailable block returns an error.
	s := c.session
	available := &cachedReader{ctx: context.Background(), session: s, track: s.tracks[0], offset: 44}
	if _, err := available.Read(make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	missing := &cachedReader{ctx: context.Background(), session: s, track: s.tracks[0], offset: 44 + 15*sectorBytes}
	if _, err := missing.Read(make([]byte, 4)); err == nil {
		t.Fatal("uncached block returned fabricated data")
	}
	time.Sleep(30 * time.Millisecond)
	if opens.Load() != 3 {
		t.Fatal("unbounded automatic retries")
	}
	c.SetPaused(true)
	if !c.Retry() {
		t.Fatal("manual retry refused")
	}
	time.Sleep(20 * time.Millisecond)
	if opens.Load() != 3 {
		t.Fatal("retry ignored pause")
	}
	c.SetPaused(false)
	waitCache(t, c, func(s Status) bool { return s.CachedBytes == s.TotalBytes })
	if c.TrackURL(1) != url || opens.Load() != 4 || c.Status().Error != "" {
		t.Fatal("manual retry discarded cache or did not recover")
	}
}

func TestRecoveryRejectsChangedDisc(t *testing.T) {
	var opens atomic.Int32
	c := &Cache{Root: t.TempDir(), Open: func(context.Context, string, []Layout) (Reader, error) {
		opens.Add(1)
		return recoveryReader{read: func(start, count int) ([]byte, error) {
			if start == 0 {
				return pcmBlock(start, count)
			}
			return nil, io.ErrUnexpectedEOF
		}}, nil
	}, ValidateRecovery: func(string, string) error { return ErrDiscChanged }}
	if err := c.Observe("disc", "fake", []Layout{{1, 0, 30}}); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	waitCache(t, c, func(s Status) bool { return s.Error != "" })
	if !errors.Is(c.Ready(), ErrDiscChanged) || opens.Load() != 1 || c.Retry() {
		t.Fatal("changed disc was used to fill old cache")
	}
}

func TestDriveAccessHeldUntilFailedReaderExits(t *testing.T) {
	closing, allowExit := make(chan struct{}), make(chan struct{})
	access := driveio.New()
	var opens atomic.Int32
	c := &Cache{Root: t.TempDir(), Access: access, Open: func(context.Context, string, []Layout) (Reader, error) {
		opens.Add(1)
		return recoveryReader{read: func(int, int) ([]byte, error) { return nil, io.ErrUnexpectedEOF }, close: func() { close(closing); <-allowExit }}, nil
	}}
	if err := c.Observe("disc", "fake", []Layout{{1, 0, 30}}); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("failed reader not closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if release, err := access.Acquire(ctx); err == nil {
		release()
		t.Fatal("physical operation overlapped helper exit")
	}
	c.Close() // A queued retry must not start after removal.
	close(allowExit)
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	release, err := access.Acquire(ctx2)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if opens.Load() != 1 {
		t.Fatal("cancelled recovery reopened drive")
	}
}

func TestRemovalCancelsRecoveryAndInvalidatesURLs(t *testing.T) {
	started := make(chan struct{})
	c := &Cache{Root: t.TempDir(), BaseURL: "http://localhost", Open: func(context.Context, string, []Layout) (Reader, error) {
		return recoveryReader{read: func(int, int) ([]byte, error) { return nil, io.ErrUnexpectedEOF }}, nil
	}, RecoveryOpen: func(ctx context.Context, _ string, _ []Layout) (Reader, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	if err := c.Observe("disc", "fake", []Layout{{1, 0, 30}}); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	s, url := c.session, c.TrackURL(1)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no recovery")
	}
	c.InvalidateUnless("replacement")
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("cancelled recovery remained alive")
	}
	response := httptest.NewRecorder()
	c.ServeHTTP(response, httptest.NewRequest("GET", url, nil))
	if response.Code != 404 {
		t.Fatal("removed disc still served")
	}
}

func TestPauseSuspendsAutomaticRecoveryAndSelectionWinsOnResume(t *testing.T) {
	started, fail := make(chan struct{}), make(chan struct{})
	opens := make(chan struct{}, 1)
	reads := make(chan int, 8)
	c := &Cache{Root: t.TempDir(), Open: func(ctx context.Context, _ string, _ []Layout) (Reader, error) {
		return recoveryReader{read: func(int, int) ([]byte, error) {
			close(started)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-fail:
				return nil, io.ErrUnexpectedEOF
			}
		}}, nil
	}, RecoveryOpen: func(context.Context, string, []Layout) (Reader, error) {
		opens <- struct{}{}
		return recoveryReader{read: func(start, count int) ([]byte, error) { reads <- start; return pcmBlock(start, count) }}, nil
	}}
	if err := c.Observe("disc", "fake", []Layout{{1, 0, 30}, {2, 30, 60}}); err != nil {
		t.Fatal(err)
	}
	defer c.Shutdown()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no read")
	}
	c.SetPaused(true)
	close(fail)
	waitCache(t, c, func(s Status) bool { return s.Recovering })
	c.Select(2)
	select {
	case <-opens:
		t.Fatal("paused recovery reopened reader")
	case <-time.After(20 * time.Millisecond):
	}
	c.SetPaused(false)
	select {
	case start := <-reads:
		if start != 30 {
			t.Fatalf("recovery ignored latest selected track: %d", start)
		}
	case <-time.After(time.Second):
		t.Fatal("recovery did not resume")
	}
}
