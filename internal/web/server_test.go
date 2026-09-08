package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/systeminfo"
)

func TestStatusIsSnapshot(t *testing.T) {
	s := &Server{}
	state := State{Disc: disc.Disc{ID: "disc", Tracks: []int{2, 3}}, MPD: map[string]string{"state": "play", "song": "0"}}
	s.Set(state)
	state.Disc.Tracks[0] = 99
	state.MPD["state"] = "stop"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/status", nil))
	var got State
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Disc.Tracks[0] != 2 || got.MPD["state"] != "play" {
		t.Fatalf("snapshot changed: %+v", got)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("status can be cached")
	}
}

func TestControls(t *testing.T) {
	for _, test := range []struct {
		body   string
		code   int
		called bool
	}{
		{`{"action":"track","track":12}`, 204, true},
		{`{"action":"stop"}`, 204, true},
		{`{"action":"usb-mix"}`, 204, true},
		{`{"action":"usb-play","song_id":"` + strings.Repeat("a", 64) + `"}`, 204, true},
		{`{"action":"usb-play","song_id":"../../etc/passwd"}`, 400, false},
		{`{"action":"usb-play"}`, 400, false},
		{`{"action":"eject"}`, 204, true},
		{`{"action":"track","track":0}`, 400, false},
		{`{"action":"track","track":100}`, 400, false},
		{`{"action":"arbitrary command"}`, 400, false},
		{`not json`, 400, false},
	} {
		t.Run(test.body, func(t *testing.T) {
			called := false
			s := &Server{Control: func(ctx context.Context, cmd Command) error {
				called = true
				if _, ok := ctx.Deadline(); !ok {
					t.Error("missing control timeout")
				}
				if cmd.Action == "track" && cmd.Track != 12 {
					t.Errorf("wrong track: %d", cmd.Track)
				}
				return nil
			}}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/control", strings.NewReader(test.body)))
			if w.Code != test.code || called != test.called {
				t.Fatalf("code %d, called %v", w.Code, called)
			}
		})
	}
}

func TestSystemEndpoint(t *testing.T) {
	s := &Server{System: func() systeminfo.Info { return systeminfo.Info{Hostname: "raspberrypi", CPUs: 4} }}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/system", nil))
	var got systeminfo.Info
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || got.Hostname != "raspberrypi" || got.CPUs != 4 {
		t.Fatalf("response: %s", w.Body.String())
	}
}

func TestControlFailureAndMethods(t *testing.T) {
	s := &Server{Control: func(context.Context, Command) error { return errors.New("MPD disconnected") }}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/control", strings.NewReader(`{"action":"play"}`)))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "MPD disconnected") {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/control", nil))
	if w.Code != 405 {
		t.Fatalf("GET performed control: %d", w.Code)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>CD player</title>") {
		t.Fatal("missing embedded page")
	}
}

func TestOutputSettingsUseController(t *testing.T) {
	var got Command
	s := &Server{Control: func(_ context.Context, cmd Command) error { got = cmd; return nil }}
	for _, body := range []string{`{"action":"outputs"}`, `{"action":"output","output":"HDMI 1"}`} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/control", strings.NewReader(body)))
		if w.Code != 204 {
			t.Fatalf("%d: %s", w.Code, w.Body.String())
		}
	}
	if got.Action != "output" || got.Output != "HDMI 1" {
		t.Fatalf("%+v", got)
	}
}
