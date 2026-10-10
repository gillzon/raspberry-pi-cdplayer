package main

import (
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/web"
)

const screenIdleTimeout = 3 * time.Minute

// Only automatic sleep wakes on observed playback. Explicit sleep keeps its
// existing stop/idle behavior, even if the last MPD status is still playing.
type screenState struct {
	asleep    bool
	automatic bool
	idleSince time.Time
}

func (s *screenState) setAsleep(asleep bool, now time.Time) {
	s.asleep = asleep
	s.automatic = false
	s.idleSince = now
}

func (s *screenState) update(now time.Time, state web.State) {
	playing := false
	switch state.Source {
	case "cd", "usb", "radio":
		playing = state.MPD["state"] == "play"
	case "spotify":
		playing = state.Spotify.Running && state.Spotify.Playback == "playing"
	}
	if playing {
		s.idleSince = now
		if s.automatic {
			s.asleep, s.automatic = false, false
		}
		return
	}
	if s.idleSince.IsZero() {
		s.idleSince = now
	}
	if !s.asleep && now.Sub(s.idleSince) >= screenIdleTimeout {
		s.asleep, s.automatic = true, true
	}
}
