package spotify

import (
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Event struct {
	Kind       string `json:"kind"`
	TrackID    string `json:"track_id"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	Cover      string `json:"cover"`
	DurationMS int64  `json:"duration_ms"`
	PositionMS int64  `json:"position_ms"`
}

func eventFromEnv() Event {
	number := func(key string) int64 {
		n, _ := strconv.ParseInt(os.Getenv(key), 10, 64)
		if n < 0 {
			return 0
		}
		return n
	}
	return Event{Kind: os.Getenv("PLAYER_EVENT"), TrackID: os.Getenv("TRACK_ID"), Title: os.Getenv("NAME"), Artist: strings.ReplaceAll(os.Getenv("ARTISTS"), "\n", ", "), Album: os.Getenv("ALBUM"), Cover: strings.Split(os.Getenv("COVERS"), "\n")[0], DurationMS: number("DURATION_MS"), PositionMS: number("POSITION_MS")}
}
func (m *Manager) Apply(e Event) {
	switch e.Kind {
	case "session_connected":
		m.State.Connected = true
		m.State.Playback = "connected"
		m.State.Now = Event{}
	case "session_disconnected":
		m.State.Connected = false
		m.State.Playback = "disconnected"
		m.State.Now = Event{}
	case "track_changed":
		u, err := url.Parse(e.Cover)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			e.Cover = ""
		}
		m.State.Now = e
	case "playing", "paused", "stopped", "loading":
		m.State.Playback = e.Kind
		if e.TrackID == m.State.Now.TrackID {
			m.State.Now.PositionMS = e.PositionMS
		}
	case "seeked", "position_correction":
		if e.TrackID == m.State.Now.TrackID {
			m.State.Now.PositionMS = e.PositionMS
		}
	}
}
