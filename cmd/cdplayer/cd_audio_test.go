package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/audio"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
)

type handoffBackend struct {
	connectErr, stopErr error
	stopped             bool
	clears              int
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
