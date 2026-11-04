package library

import (
	"fmt"
	"testing"
	"time"
)

func TestPlayCounterThresholdPauseSkipReplayAndSources(t *testing.T) {
	var p PlayCounter
	start := time.Now()
	track := &Track{ID: "one", Duration: 180}
	observe := func(wall, elapsed int, state string) string {
		return p.Observe("usb", track, map[string]string{"state": state, "elapsed": fmt.Sprint(elapsed), "songid": "1"}, start.Add(time.Duration(wall)*time.Second))
	}
	for i := 0; i < 30; i++ {
		if got := observe(i, i, "play"); got != "" {
			t.Fatal("counted early", i)
		}
	}
	if got := observe(30, 30, "play"); got != "one" {
		t.Fatal("missing play", got)
	}
	for i := 31; i < 40; i++ {
		if got := observe(i, i, "play"); got != "" {
			t.Fatal("counted twice")
		}
	}
	observe(40, 40, "pause")
	observe(50, 40, "pause")
	observe(51, 40, "play")
	if got := observe(52, 41, "play"); got != "" {
		t.Fatal("resume counted again")
	}
	observe(53, 0, "play")
	for i := 1; i < 30; i++ {
		if got := observe(53+i, i, "play"); got != "" {
			t.Fatal("repeat early")
		}
	}
	if got := observe(83, 30, "play"); got != "one" {
		t.Fatal("repeat missing")
	}
	// Switching away resets the session; jumping to 100 seconds is not listening.
	p.Observe("radio", nil, nil, start.Add(84*time.Second))
	observe(85, 0, "play")
	if got := observe(86, 100, "play"); got != "" {
		t.Fatal("seek counted")
	}
	for i := 1; i < 30; i++ {
		if got := observe(86+i, 100+i, "play"); got != "" {
			t.Fatal("seek duration counted")
		}
	}
	if got := observe(116, 130, "play"); got != "one" {
		t.Fatal("listened play missing")
	}
}

func TestPlayCounterShortSongsAndQueueChanges(t *testing.T) {
	var p PlayCounter
	now := time.Now()
	track := &Track{ID: "short", Duration: 10}
	for i := 0; i <= 5; i++ {
		got := p.Observe("usb", track, map[string]string{"state": "play", "elapsed": fmt.Sprint(i), "songid": "1"}, now.Add(time.Duration(i)*time.Second))
		if (got != "") != (i == 5) {
			t.Fatalf("at %d: %s", i, got)
		}
	}
	if got := p.Observe("usb", track, map[string]string{"state": "play", "elapsed": "6", "songid": "2"}, now.Add(6*time.Second)); got != "" {
		t.Fatal("new queue inherited listening time")
	}
	p.Observe("usb", track, map[string]string{"state": "stop"}, now.Add(7*time.Second))
	if p.counted || p.listened != 0 {
		t.Fatal("stop did not reset")
	}
}
