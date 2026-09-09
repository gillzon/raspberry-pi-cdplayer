package main

import (
	"github.com/gillzon/raspberry-pi-cdplayer/internal/spotify"
	"testing"
)

func TestSpotifyWakeEvents(t *testing.T) {
	for _, test := range []struct {
		event, previous string
		wake            bool
	}{
		{"session_connected", "stopped", true}, {"sink", "playing", true},
		{"playing", "paused", true}, {"playing", "playing", false},
		{"track_changed", "playing", false}, {"position_correction", "playing", false},
		{"session_disconnected", "playing", false}, {"paused", "playing", false},
	} {
		if got := spotifyWakesScreen(spotify.Event{Kind: test.event}, test.previous); got != test.wake {
			t.Errorf("%+v: got %v", test, got)
		}
	}
}
