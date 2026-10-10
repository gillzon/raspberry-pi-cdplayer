package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/audio"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/web"
)

type handoffBackend struct {
	connectErr, stopErr error
	stopped             bool
	clears              int
}

type audioControlFunc func(string, int) error

func (f audioControlFunc) Control(action string, position int) error { return f(action, position) }

type firstBlockReader struct{ ctx context.Context }

func (r firstBlockReader) Read(sector, count int) ([]byte, error) {
	if sector == 0 {
		return make([]byte, count*2352), nil
	}
	<-r.ctx.Done()
	return nil, r.ctx.Err()
}
func (r firstBlockReader) Close() error { return nil }

func readyAudioCache(t *testing.T) *audio.Cache {
	t.Helper()
	wake := make(chan struct{}, 1)
	c := &audio.Cache{Root: t.TempDir(), ReadyNotify: wake, Open: func(ctx context.Context, _ string, _ []audio.Layout) (audio.Reader, error) {
		return firstBlockReader{ctx}, nil
	}}
	t.Cleanup(c.Shutdown)
	if err := c.Observe("disc", "fake", []audio.Layout{{Number: 1, Start: 0, End: 300}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Fatal("cache not ready")
	}
	return c
}

func TestCacheFollowsAcknowledgedControls(t *testing.T) {
	for _, action := range []string{"pause", "stop", "play", "track"} {
		for _, fail := range []bool{false, true} {
			t.Run(action+map[bool]string{false: "/accepted", true: "/failed"}[fail], func(t *testing.T) {
				cache := readyAudioCache(t)
				resuming := action == "play" || action == "track"
				cache.SetPaused(resuming)
				failure := errors.New("MPD unavailable")
				backend := audioControlFunc(func(got string, position int) error {
					if got != action || position != 0 {
						t.Fatalf("wrong control: %s %d", got, position)
					}
					if resuming && cache.Status().Paused {
						t.Fatal("MPD asked to play while its audio reader is paused")
					}
					if fail {
						return failure
					}
					return nil
				})
				err := controlCachedAudio(cache, backend, action, 0)
				wantPaused := !resuming
				if fail {
					wantPaused = resuming
				}
				if errors.Is(err, failure) != fail || cache.Status().Paused != wantPaused {
					t.Fatalf("err=%v paused=%v; want failed=%v paused=%v", err, cache.Status().Paused, fail, wantPaused)
				}
			})
		}
	}
}

func TestStoppedOrPausedCDStaysSuspendedThroughScreenSleep(t *testing.T) {
	for _, state := range []string{"pause", "stop"} {
		t.Run(state, func(t *testing.T) {
			cache := readyAudioCache(t)
			start := time.Unix(1000, 0)
			screen := screenState{idleSince: start}
			status := map[string]string{"state": state}
			for elapsed := time.Duration(0); elapsed <= screenIdleTimeout; elapsed += time.Second {
				syncCachedAudio(cache, "cd", status)
				screen.update(start.Add(elapsed), web.State{Source: "cd", MPD: status})
			}
			if !cache.Status().Paused || !screen.asleep {
				t.Fatal("idle CD did not keep caching suspended and put screen to sleep")
			}
			status["state"] = "play"
			syncCachedAudio(cache, "cd", status)
			screen.update(start.Add(4*time.Minute), web.State{Source: "cd", MPD: status})
			if cache.Status().Paused || screen.asleep {
				t.Fatal("resumed playback did not wake screen and cache")
			}
		})
	}
}

func (b *handoffBackend) Connect(context.Context) (bool, error) { return true, b.connectErr }
func (b *handoffBackend) Clear() error {
	b.clears++
	b.stopped = b.stopErr == nil
	return b.stopErr
}

func TestCachedReaderWaitsForMPDRelease(t *testing.T) {
	for _, failConnect := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "connect"}[failConnect], func(t *testing.T) {
			backend := &handoffBackend{}
			failure := errors.New("MPD still owns drive")
			if failConnect {
				backend.connectErr = failure
			} else {
				backend.stopErr = failure
			}
			opened := make(chan bool, 1)
			cache := &audio.Cache{Root: t.TempDir(), Open: func(ctx context.Context, _ string, _ []audio.Layout) (audio.Reader, error) {
				opened <- backend.stopped
				<-ctx.Done()
				return nil, ctx.Err()
			}}
			defer cache.Shutdown()
			d := disc.Disc{ID: "disc", Layout: []disc.TrackLayout{{Number: 1, Start: 0, End: 75}}}
			if err := prepareCachedDisc(context.Background(), backend, cache, d, "/dev/fake"); !errors.Is(err, failure) {
				t.Fatalf("lost handoff failure: %v", err)
			}
			if cache.Status().DiscID != "" {
				t.Fatal("created cache before releasing MPD")
			}
			backend.connectErr, backend.stopErr = nil, nil
			if err := prepareCachedDisc(context.Background(), backend, cache, d, "/dev/fake"); !errors.Is(err, audio.ErrPreparing) {
				t.Fatal(err)
			}
			select {
			case stopped := <-opened:
				if !stopped {
					t.Fatal("reader opened while MPD owned drive")
				}
			case <-time.After(time.Second):
				t.Fatal("reader never opened after MPD released drive")
			}
			clears := backend.clears
			prepareCachedDisc(context.Background(), backend, cache, d, "/dev/fake")
			if backend.clears != clears {
				t.Fatal("routine preparation stopped MPD again")
			}
		})
	}
}

func TestHardwareCDNavigationResolvesCacheTrack(t *testing.T) {
	for _, tc := range []struct {
		action, song string
		want         int
	}{
		{"next", "0", 3}, {"previous", "2", 3},
		{"next", "2", 0}, {"previous", "0", 0}, {"next", "", 0},
	} {
		got, err := adjacentCDTrack(tc.action, map[string]string{"song": tc.song}, []int{1, 3, 7})
		if got != tc.want || (err != nil) != (tc.want == 0) {
			t.Errorf("%+v: got %d, %v", tc, got, err)
		}
	}
}
