package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
)

type record struct {
	Info Info
	Art  []byte
}

type Manager struct {
	mu                        sync.RWMutex
	current                   record
	desired                   disc.Disc
	cancel                    context.CancelFunc
	taskContext               context.Context
	wake                      chan struct{}
	cacheDir                  string
	client                    *http.Client
	musicBrainz, coverArchive string
	nextRequest               time.Time
}

func New(cacheDir string) *Manager {
	return &Manager{cacheDir: cacheDir, wake: make(chan struct{}, 1), client: &http.Client{Timeout: 15 * time.Second}, musicBrainz: "https://musicbrainz.org", coverArchive: "https://coverartarchive.org"}
}

// Observe only updates memory and cancels stale work; it never performs I/O.
func (m *Manager) Observe(ctx context.Context, d disc.Disc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d.ID == m.desired.ID {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.desired = d
	m.taskContext, m.cancel = context.WithCancel(ctx)
	m.current = record{Info: Info{DiscID: d.ID, Status: "loading"}}
	if d.ID == "" {
		m.current.Info.Status = "empty"
		return
	}
	if d.MusicBrainzID == "" {
		m.current.Info.Status = "unavailable"
		m.current.Info.Message = "Album lookup is currently available for standard audio CDs"
		return
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) Snapshot(id string) Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current.Info.DiscID != id {
		return Info{DiscID: id, Status: "loading"}
	}
	return m.current.Info // published records/maps are immutable
}

func (m *Manager) Art(id string) []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current.Info.DiscID != id {
		return nil
	}
	return m.current.Art
}

func (m *Manager) publish(ctx context.Context, r record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() == nil && m.desired.ID == r.Info.DiscID {
		m.current = r
	}
}

func (m *Manager) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		}
		m.mu.RLock()
		d, taskCtx := m.desired, m.taskContext
		m.mu.RUnlock()
		if taskCtx == nil || taskCtx.Err() != nil || d.MusicBrainzID == "" {
			continue
		}
		r := m.load(d.ID)
		if r.Info.Status != "ready" {
			info, err := m.lookup(taskCtx, d)
			if err != nil {
				m.publish(taskCtx, record{Info: Info{DiscID: d.ID, Status: "error", Message: "Album lookup unavailable; reinsert the disc to retry"}})
				if taskCtx.Err() == nil {
					slog.Warn("album lookup failed", "error", err)
				}
				continue
			}
			r.Info = info
		}
		m.publish(taskCtx, r) // titles are available before the artwork request
		if r.Info.Status == "ready" && len(r.Art) == 0 {
			art, status, err := m.get(taskCtx, m.coverArchive+"/release/"+r.Info.ReleaseID+"/front-500", 4<<20)
			if err == nil && status == 200 {
				kind := http.DetectContentType(art)
				if kind == "image/jpeg" || kind == "image/png" || kind == "image/webp" {
					r.Art = art
					r.Info.CoverURL = "/api/art/" + d.ID
				}
			}
		}
		if taskCtx.Err() == nil && r.Info.Status == "ready" {
			m.save(d.ID, r)
		}
		m.publish(taskCtx, r)
	}
}

func (m *Manager) load(id string) record {
	if m.cacheDir == "" {
		return record{}
	}
	b, err := os.ReadFile(filepath.Join(m.cacheDir, id+".json"))
	if err != nil {
		return record{}
	}
	var r record
	if json.Unmarshal(b, &r) != nil || r.Info.DiscID != id {
		return record{}
	}
	return r
}

func (m *Manager) save(id string, r record) {
	if m.cacheDir == "" {
		return
	}
	if err := m.write(id, r); err != nil {
		slog.Warn("could not cache album information", "error", err)
	}
}

func (m *Manager) write(id string, r record) error {
	if err := os.MkdirAll(m.cacheDir, 0755); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(m.cacheDir, "album-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(m.cacheDir, id+".json")); err != nil {
		return fmt.Errorf("save album: %w", err)
	}
	return nil
}
