package spotify

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestHookWaitsForSuccessfulStop(t *testing.T) {
	t.Setenv("PLAYER_EVENT", "sink")
	t.Setenv("SINK_STATUS", "running")
	t.Setenv("CDPLAYER_SPOTIFY_CALLBACK", "http://localhost/api/control")
	t.Setenv("CDPLAYER_SPOTIFY_TOKEN", "generation")
	old := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = old })
	calls := 0
	http.DefaultClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"token":"generation"`) {
			t.Fatal("missing generation token")
		}
		status := http.StatusServiceUnavailable
		if calls == 2 {
			status = http.StatusNoContent
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	if err := Hook(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("sink gate released before successful stop")
	}
}
func TestReceiverRestartInvalidatesCallbacks(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "librespot")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Binary: binary, State: State{Enabled: true, Name: "Test"}}
	defer m.Stop()
	m.Tick()
	token := m.Token
	if !m.Accept(token) || m.Accept("") || m.Accept("wrong") {
		t.Fatal("incorrect callback validation")
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if m.Accept(token) {
		t.Fatal("stopped process callback accepted")
	}
	m.Tick()
	if !m.State.Running || m.Token == token || m.Accept(token) {
		t.Fatal("restart retained old callback generation")
	}
}
func TestMissingReceiverBacksOff(t *testing.T) {
	m := &Manager{Binary: "/nonexistent/librespot", State: State{Enabled: true}}
	m.Tick()
	retry := m.retry
	if m.State.Error == "" || m.State.Running || m.Token != "" {
		t.Fatal("failed receiver reported ready")
	}
	m.Tick()
	if m.retry != retry {
		t.Fatal("retried without backoff")
	}
}
