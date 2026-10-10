package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/audio"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
)

type cdBackend interface {
	Connect(context.Context) (bool, error)
	Clear() error
}

// MPD may have restored a direct cdda:// queue from an older installation.
// Release that reader before opening ours, even when stopping MPD needs retries.
func prepareCachedDisc(ctx context.Context, backend cdBackend, cache *audio.Cache, d disc.Disc, device string) error {
	if d.ID != "" && len(d.Layout) > 0 && cache.Status().DiscID != d.ID {
		if _, err := backend.Connect(ctx); err != nil {
			return err
		}
		if err := backend.Clear(); err != nil {
			return fmt.Errorf("release MPD before opening CD reader: %w", err)
		}
	}
	layout := make([]audio.Layout, 0, len(d.Layout))
	for _, t := range d.Layout {
		layout = append(layout, audio.Layout{Number: t.Number, Start: t.Start, End: t.End})
	}
	if err := cache.Observe(d.ID, device, layout); err != nil {
		return err
	}
	return cache.Ready()
}

// Hardware next/previous must select the same cache track as the browser does.
// CD track numbers need not be consecutive on mixed-mode discs.
func adjacentCDTrack(action string, status map[string]string, tracks []int) (int, error) {
	position, err := strconv.Atoi(status["song"])
	if err != nil || position < 0 || position >= len(tracks) {
		return 0, fmt.Errorf("current CD track unavailable; select a track first")
	}
	if action == "next" {
		position++
	} else {
		position--
	}
	if position < 0 || position >= len(tracks) {
		return 0, fmt.Errorf("no %s CD track", action)
	}
	return tracks[position], nil
}
