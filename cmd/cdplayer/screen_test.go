package main

import (
	"testing"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/spotify"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/web"
)

func TestScreenIdleTimeout(t *testing.T) {
	for _, source := range []string{"cd", "usb", "radio", "spotify", "idle"} {
		for _, playback := range []string{"", "pause", "stop", "connected", "paused", "stopped", "loading"} {
			t.Run(source+"/"+playback, func(t *testing.T) {
				start := time.Unix(1000, 0)
				screen := screenState{}
				state := web.State{Source: source, MPD: map[string]string{"state": playback}, Spotify: spotify.State{Running: true, Playback: playback}}
				// Frequent status publications must not postpone sleep.
				for elapsed := time.Duration(0); elapsed < 3*time.Minute; elapsed += time.Second {
					screen.update(start.Add(elapsed), state)
					if screen.asleep {
						t.Fatalf("slept early at %v", elapsed)
					}
				}
				screen.update(start.Add(3*time.Minute), state)
				if !screen.asleep || !screen.automatic {
					t.Fatal("did not automatically sleep after three idle minutes")
				}
			})
		}
	}
}

func TestScreenPlaybackKeepsAwakeAndResumes(t *testing.T) {
	for _, source := range []string{"cd", "usb", "radio", "spotify"} {
		t.Run(source, func(t *testing.T) {
			start := time.Unix(1000, 0)
			screen := screenState{idleSince: start}
			playing := web.State{Source: source, MPD: map[string]string{"state": "play"}, Spotify: spotify.State{Running: true, Playback: "playing"}}
			paused := web.State{Source: source, MPD: map[string]string{"state": "pause"}, Spotify: spotify.State{Running: true, Playback: "paused"}}
			for i := 0; i <= 10; i++ {
				screen.update(start.Add(time.Duration(i)*time.Minute), playing)
				if screen.asleep {
					t.Fatal("slept during playback")
				}
			}
			screen.update(start.Add(12*time.Minute), paused)
			if screen.asleep {
				t.Fatal("did not reset timeout during playback")
			}
			screen.update(start.Add(13*time.Minute), paused)
			if !screen.asleep {
				t.Fatal("did not sleep after pause")
			}
			screen.update(start.Add(14*time.Minute), playing)
			if screen.asleep {
				t.Fatal("did not wake when playback resumed")
			}
		})
	}
}

func TestScreenManualWakeRestartsTimeout(t *testing.T) {
	start := time.Unix(1000, 0)
	screen := screenState{idleSince: start}
	screen.update(start.Add(3*time.Minute), web.State{})
	wake := start.Add(10 * time.Minute)
	screen.setAsleep(false, wake)
	screen.update(wake.Add(3*time.Minute-time.Second), web.State{})
	if screen.asleep {
		t.Fatal("manual wake did not grant another three minutes")
	}
	screen.update(wake.Add(3*time.Minute), web.State{})
	if !screen.asleep {
		t.Fatal("did not sleep again after manual wake")
	}
}

func TestScreenManualSleepIgnoresStalePlayback(t *testing.T) {
	start := time.Unix(1000, 0)
	screen := screenState{}
	screen.setAsleep(true, start)
	screen.update(start.Add(time.Second), web.State{Source: "cd", MPD: map[string]string{"state": "play"}})
	if !screen.asleep {
		t.Fatal("stale playback undid manual sleep")
	}
}

func TestScreenIgnoresPlaybackFromInactiveSource(t *testing.T) {
	start := time.Unix(1000, 0)
	for _, state := range []web.State{
		{Source: "spotify", MPD: map[string]string{"state": "play"}},
		{Source: "cd", Spotify: spotify.State{Running: true, Playback: "playing"}},
		{Source: "idle", MPD: map[string]string{"state": "play"}},
		{Source: "spotify", Spotify: spotify.State{Running: false, Playback: "playing"}},
	} {
		screen := screenState{idleSince: start}
		screen.update(start.Add(3*time.Minute), state)
		if !screen.asleep {
			t.Errorf("inactive playback kept screen awake: %+v", state)
		}
	}
}

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
