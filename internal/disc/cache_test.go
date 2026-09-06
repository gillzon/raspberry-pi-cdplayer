package disc

import (
	"errors"
	"testing"
)

func TestReadyPollsReadTOCOnlyOnce(t *testing.T) {
	var cache tocCache
	reads := 0
	load := func() (Disc, error) { reads++; return Disc{ID: "album", Tracks: []int{1, 2}}, nil }
	for i := 0; i < 100; i++ {
		d, err := cache.read(4, load)
		if err != nil || d.ID != "album" {
			t.Fatalf("read: %+v %v", d, err)
		}
	}
	if reads != 1 {
		t.Fatalf("TOC read %d times during unchanged playback", reads)
	}
}

func TestCacheInvalidatedWhenDriveLeavesReadyState(t *testing.T) {
	for _, status := range []int{1, 2, 3, 0} {
		var cache tocCache
		load := func() (Disc, error) { return Disc{ID: "first"}, nil }
		if _, err := cache.read(4, load); err != nil {
			t.Fatal(err)
		}
		_, _ = cache.read(status, func() (Disc, error) { t.Fatal("read TOC before ready"); return Disc{}, nil })
		d, err := cache.read(4, func() (Disc, error) { return Disc{ID: "replacement"}, nil })
		if err != nil || d.ID != "replacement" {
			t.Fatalf("status %d retained stale disc: %+v %v", status, d, err)
		}
	}
}

func TestFailedTOCReadIsRetried(t *testing.T) {
	var cache tocCache
	if _, err := cache.read(4, func() (Disc, error) { return Disc{}, errors.New("still spinning up") }); err == nil {
		t.Fatal("missing error")
	}
	d, err := cache.read(4, func() (Disc, error) { return Disc{ID: "ready"}, nil })
	if err != nil || d.ID != "ready" {
		t.Fatalf("failed to recover: %+v %v", d, err)
	}
}
