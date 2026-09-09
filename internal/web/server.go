// Package web serves the LAN interface. Playback operations are sent to the
// controller loop so HTTP requests never share the MPD protocol connection.
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/audio"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/library"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/metadata"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/mpd"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/player"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/radio"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/spotify"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/systeminfo"
)

//go:embed index.html
var page []byte

//go:embed display.html
var displayPage []byte

var displayVersion = fmt.Sprintf("%x", sha256.Sum256(displayPage))

//go:embed navigation.js
var navigation []byte

//go:embed library.js
var libraryScript []byte

type State struct {
	ScreenAsleep   bool                  `json:"screen_asleep"`
	DisplayVersion string                `json:"display_version"`
	Radio          *radio.Station        `json:"radio,omitempty"`
	USBMix         bool                  `json:"usb_mix"`
	USB            *library.Track        `json:"usb,omitempty"`
	USBQueueLength int                   `json:"usb_queue_length"`
	Outputs        []mpd.Output          `json:"outputs"`
	Selection      player.TrackSelection `json:"selection"`
	Audio          audio.Status          `json:"audio"`
	Source         string                `json:"source"`
	Spotify        spotify.State         `json:"spotify"`
	Device         string                `json:"device"`
	Disc           disc.Disc             `json:"disc"`
	MPD            map[string]string     `json:"mpd"`
	Error          string                `json:"error"`
	Updated        time.Time             `json:"updated"`
	Metadata       metadata.Info         `json:"metadata"`
}

type Command struct {
	Station string        `json:"station"`
	SongID  string        `json:"song_id"`
	Output  string        `json:"output"`
	Event   spotify.Event `json:"event"`
	Token   string        `json:"token"`
	Action  string        `json:"action"`
	Track   int           `json:"track"`
	DiscID  string        `json:"disc_id"`
}

type Server struct {
	Library   *library.Library
	mu        sync.RWMutex
	state     State
	Control   func(context.Context, Command) error
	Selection func() player.TrackSelection
	System    func() systeminfo.Info
	Metadata  *metadata.Manager
}

func (s *Server) Set(state State) {
	state.Disc.Tracks = slices.Clone(state.Disc.Tracks)
	state.MPD = maps.Clone(state.MPD)
	state.Outputs = slices.Clone(state.Outputs)
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /library.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(libraryScript)
	})
	mux.HandleFunc("GET /api/library", func(w http.ResponseWriter, r *http.Request) {
		if s.Library == nil {
			http.Error(w, "USB music is disabled; configure a music directory", 503)
			return
		}
		offset := 0
		if value := r.URL.Query().Get("offset"); value != "" {
			var err error
			offset, err = strconv.Atoi(value)
			if err != nil || offset < 0 || offset > 10000000 {
				http.Error(w, "Invalid offset", 400)
				return
			}
		}
		query := r.URL.Query().Get("q")
		if len(query) > 512 {
			http.Error(w, "Search is too long", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		result, err := s.Library.Search(ctx, query, offset)
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /api/library/refresh", func(w http.ResponseWriter, r *http.Request) {
		if s.Library == nil {
			http.Error(w, "USB music is disabled", 503)
			return
		}
		s.Library.Refresh()
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /api/library/art/{id}", func(w http.ResponseWriter, r *http.Request) {
		if s.Library == nil || !songIDPattern.MatchString(r.PathValue("id")) {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		data, err := s.Library.Art(ctx, r.PathValue("id"))
		if err != nil || len(data) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(data))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
		w.Write(data)
	})
	mux.HandleFunc("GET /display", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(bytes.ReplaceAll(displayPage, []byte("__DISPLAY_VERSION__"), []byte(displayVersion)))
	})
	mux.HandleFunc("GET /navigation.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(navigation)
	})
	mux.HandleFunc("GET /api/art/{id}", func(w http.ResponseWriter, r *http.Request) {
		if s.Metadata == nil {
			http.NotFound(w, r)
			return
		}
		art := s.Metadata.Art(r.PathValue("id"))
		if len(art) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(art))
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Write(art)
	})
	mux.HandleFunc("GET /api/system", func(w http.ResponseWriter, r *http.Request) {
		if s.System == nil {
			http.Error(w, "System information unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(s.System())
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	})
	mux.HandleFunc("GET /api/radio", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(radio.Stations())
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		s.mu.RLock()
		state := s.state
		s.mu.RUnlock()
		state.DisplayVersion = displayVersion
		if s.Selection != nil {
			state.Selection = s.Selection()
		}
		if s.Metadata != nil {
			state.Metadata = s.Metadata.Snapshot(state.Disc.ID)
		}
		json.NewEncoder(w).Encode(state)
	})
	mux.HandleFunc("POST /api/control", func(w http.ResponseWriter, r *http.Request) {
		var cmd Command
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&cmd); err != nil {
			http.Error(w, "Invalid command", http.StatusBadRequest)
			return
		}
		switch cmd.Action {
		case "radio-play":
			if _, ok := radio.Find(cmd.Station); !ok {
				http.Error(w, "Unknown radio station", 400)
				return
			}
		case "screen-sleep", "screen-wake", "screen-toggle", "source-usb", "source-next", "source-radio", "source-spotify", "toggle", "usb-mix", "outputs", "output", "spotify-event", "spotify-start", "source-cd", "play", "pause", "stop", "next", "previous", "eject":
		case "usb-play":
			if !songIDPattern.MatchString(cmd.SongID) {
				http.Error(w, "Invalid song", 400)
				return
			}
		case "track":
			if cmd.Track < 1 || cmd.Track > 99 {
				http.Error(w, "Invalid track", 400)
				return
			}
		default:
			http.Error(w, "Unknown action", 400)
			return
		}
		timeout := 25 * time.Second
		if cmd.Action == "usb-mix" || cmd.Action == "play" || cmd.Action == "toggle" {
			timeout = 2 * time.Minute
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if s.Control == nil {
			http.Error(w, "Player unavailable", 503)
			return
		}
		if err := s.Control(ctx, cmd); err != nil {
			http.Error(w, fmt.Sprintf("Control failed: %v", err), 503)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

var songIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
