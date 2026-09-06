// Package spotify manages an optional librespot receiver. Manager methods run
// on the player loop; event hooks acknowledge sink startup only after MPD stops.
package spotify

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type State struct {
	Connected bool   `json:"connected"`
	Playback  string `json:"playback"`
	Now       Event  `json:"now"`
	Enabled   bool   `json:"enabled"`
	Running   bool   `json:"running"`
	Name      string `json:"name"`
	Error     string `json:"error"`
}
type Manager struct {
	Binary, Device, Callback string
	State                    State
	Token                    string
	cmd                      *exec.Cmd
	done                     chan error
	retry                    time.Time
}

func (m *Manager) Tick() {
	if !m.State.Enabled {
		return
	}
	if m.cmd != nil {
		select {
		case err := <-m.done:
			m.cmd = nil
			m.Token = ""
			m.State.Running = false
			m.Apply(Event{Kind: "session_disconnected"})
			m.State.Error = fmt.Sprintf("Spotify receiver exited: %v", err)
			m.retry = time.Now().Add(5 * time.Second)
		default:
			return
		}
	}
	if time.Now().Before(m.retry) {
		return
	}
	if err := m.start(); err != nil {
		m.State.Error = err.Error()
		m.retry = time.Now().Add(5 * time.Second)
	}
}
func (m *Manager) start() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if strings.ContainsAny(exe, " \t\r\n") {
		return fmt.Errorf("Spotify hook executable path must not contain whitespace")
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return err
	}
	m.Token = hex.EncodeToString(token)
	cmd := exec.Command(m.Binary, "--name", m.State.Name, "--backend", "alsa", "--device", m.Device, "--initial-volume", "50", "--disable-audio-cache", "--disable-credential-cache", "--emit-sink-events", "--onevent", exe+" -spotify-event")
	cmd.Env = append(os.Environ(), "CDPLAYER_SPOTIFY_CALLBACK="+m.Callback, "CDPLAYER_SPOTIFY_TOKEN="+m.Token)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	protectChild(cmd)
	if err = cmd.Start(); err != nil {
		m.Token = ""
		return fmt.Errorf("start librespot: %w", err)
	}
	m.cmd = cmd
	m.done = make(chan error, 1)
	done := m.done
	go func() { done <- cmd.Wait() }()
	m.State.Running = true
	m.State.Error = ""
	return nil
}
func (m *Manager) Stop() error {
	m.Token = ""
	if m.cmd == nil {
		return nil
	}
	// Kill our own receiver before permitting MPD to reopen ALSA. Its hook may
	// be waiting for this very controller loop, so graceful shutdown can deadlock.
	if err := m.cmd.Process.Kill(); err != nil && err != os.ErrProcessDone {
		return err
	}
	select {
	case <-m.done:
		m.cmd = nil
		m.State.Running = false
		m.Apply(Event{Kind: "session_disconnected"})
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("Spotify receiver has not exited; CD remains stopped")
	}
}
func (m *Manager) Accept(token string) bool {
	return m.State.Running && m.Token != "" && token == m.Token
}

// Hook requests CD interruption as soon as a Spotify session connects. The
// blocking sink hook repeats the handoff before audio opens, covering races.
// Disconnect and pause deliberately leave source selection unchanged.
func Hook() error {
	e := eventFromEnv()
	event := e.Kind
	handoff := event == "session_connected" || (event == "sink" && os.Getenv("SINK_STATUS") == "running")
	if !handoff {
		switch event {
		case "session_disconnected", "track_changed", "playing", "paused", "stopped", "loading", "seeked", "position_correction":
		default:
			return nil
		}
	}
	action := "spotify-event"
	if handoff {
		action = "spotify-start"
	}
	parent := os.Getppid()
	body, _ := json.Marshal(struct {
		Action string `json:"action"`
		Token  string `json:"token"`
		Event  Event  `json:"event"`
	}{action, os.Getenv("CDPLAYER_SPOTIFY_TOKEN"), e})
	url := os.Getenv("CDPLAYER_SPOTIFY_CALLBACK")
	waitingLogged := false
	for os.Getppid() == parent && parent > 1 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			resp, e := http.DefaultClient.Do(req)
			if e == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusNoContent {
					cancel()
					return nil
				}
			}
		}
		cancel()
		if !handoff && event != "session_disconnected" {
			return fmt.Errorf("Spotify event %s could not reach player", event)
		}
		if !waitingLogged {
			slog.Warn("waiting to deliver Spotify event to player", "event", event)
			waitingLogged = true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("Spotify receiver exited before audio handoff")
}
