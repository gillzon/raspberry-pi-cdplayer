// Package web serves the LAN interface. Playback operations are sent to the
// controller loop so HTTP requests never share the MPD protocol connection.
package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/systeminfo"
)

//go:embed index.html
var page []byte

type State struct {
	Device  string            `json:"device"`
	Disc    disc.Disc         `json:"disc"`
	MPD     map[string]string `json:"mpd"`
	Error   string            `json:"error"`
	Updated time.Time         `json:"updated"`
}

type Command struct {
	Action string `json:"action"`
	Track  int    `json:"track"`
}

type Server struct {
	mu      sync.RWMutex
	state   State
	Control func(context.Context, Command) error
	System  func() systeminfo.Info
}

func (s *Server) Set(state State) {
	state.Disc.Tracks = slices.Clone(state.Disc.Tracks)
	state.MPD = maps.Clone(state.MPD)
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
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
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		s.mu.RLock()
		state := s.state
		s.mu.RUnlock()
		json.NewEncoder(w).Encode(state)
	})
	mux.HandleFunc("POST /api/control", func(w http.ResponseWriter, r *http.Request) {
		var cmd Command
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&cmd); err != nil {
			http.Error(w, "Invalid command", http.StatusBadRequest)
			return
		}
		switch cmd.Action {
		case "play", "pause", "stop", "next", "previous", "eject":
		case "track":
			if cmd.Track < 1 || cmd.Track > 99 {
				http.Error(w, "Invalid track", 400)
				return
			}
		default:
			http.Error(w, "Unknown action", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
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
