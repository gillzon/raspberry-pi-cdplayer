// Package library indexes a mounted music directory without modifying it.
package library

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed index.py
var helper string

type Track struct {
	ID       string  `json:"id"`
	Path     string  `json:"path,omitempty"`
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album"`
	Track    int     `json:"track"`
	Duration float64 `json:"duration"`
	CoverURL string  `json:"cover_url"`
}

type Status struct {
	Enabled      bool      `json:"enabled"`
	Scanning     bool      `json:"scanning"`
	Count        int       `json:"count"`
	Warnings     int       `json:"warnings"`
	Error        string    `json:"error"`
	Updated      time.Time `json:"updated"`
	Phase        string    `json:"phase"`
	Total        int       `json:"total"`
	Processed    int       `json:"processed"`
	Checkpointed int       `json:"checkpointed"`
	Percent      int       `json:"percent"`
}

type Results struct {
	Tracks []Track `json:"tracks"`
	Total  int     `json:"total"`
	Status Status  `json:"status"`
}

type Library struct {
	Root, Database, BaseURL string
	mu                      sync.RWMutex
	status                  Status
	wake                    chan struct{}
}

func New(root, database string) (*Library, error) {
	if !filepath.IsAbs(root) || database == "" {
		return nil, fmt.Errorf("USB music requires an absolute music directory and a cache directory")
	}
	if err := os.MkdirAll(filepath.Dir(database), 0750); err != nil {
		return nil, err
	}
	return &Library{Root: filepath.Clean(root), Database: database, wake: make(chan struct{}, 1), status: Status{Enabled: true}}, nil
}

func (l *Library) run(ctx context.Context, action string, args ...string) ([]byte, error) {
	if action != "scan" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
	}
	command := exec.CommandContext(ctx, "python3", append([]string{"-c", helper, action, l.Database, l.Root}, args...)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("USB library: %s (%w)", strings.TrimSpace(stderr.String()), err)
	}
	return data, nil
}

func (l *Library) Snapshot() Status { l.mu.RLock(); defer l.mu.RUnlock(); return l.status }

// A large disk can take hours. Stream committed progress, bounded by the service
// context rather than the short timeout used for ordinary library requests.
func (l *Library) runScan(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-c", helper, "scan-progress", l.Database, l.Root)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err = command.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	var progress Status
	var decodeErr error
	for scanner.Scan() {
		if decodeErr = json.Unmarshal(scanner.Bytes(), &progress); decodeErr != nil {
			cancel()
			break
		}
		l.mu.Lock()
		progress.Enabled, progress.Scanning = true, true
		progress.Updated = l.status.Updated
		l.status = progress
		l.mu.Unlock()
	}
	if scanner.Err() != nil {
		cancel()
	}
	waitErr := command.Wait()
	if decodeErr != nil {
		return fmt.Errorf("USB scan progress: %w", decodeErr)
	}
	if scanner.Err() != nil {
		return fmt.Errorf("USB scan progress: %w", scanner.Err())
	}
	if waitErr != nil {
		return fmt.Errorf("USB library: %s (%w)", strings.TrimSpace(stderr.String()), waitErr)
	}
	if progress.Phase != "complete" {
		return fmt.Errorf("USB scan exited without completing; saved batches retained")
	}
	return nil
}
func (l *Library) Refresh() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}
func (l *Library) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		l.mu.Lock()
		l.status.Scanning = true
		l.status.Phase = "discovering"
		l.status.Total, l.status.Processed, l.status.Checkpointed, l.status.Percent = 0, 0, 0, 0
		l.status.Error = ""
		l.mu.Unlock()
		err := l.runScan(ctx)
		l.mu.Lock()
		l.status.Scanning = false
		if err != nil {
			l.status.Error = err.Error()
			l.status.Phase = "interrupted"
		} else {
			l.status.Error = ""
			l.status.Updated = time.Now()
		}
		l.mu.Unlock()
		// Start the interval after completion; a long scan must not immediately
		// trigger another scan because a ticker has been pending for hours.
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-l.wake:
		}
		timer.Stop()
	}
}
func (l *Library) Search(ctx context.Context, query string, offset int) (Results, error) {
	data, err := l.run(ctx, "search", query, fmt.Sprint(offset))
	var result Results
	if err == nil {
		err = json.Unmarshal(data, &result)
	}
	result.Status = l.Snapshot()
	return result, err
}
func (l *Library) Get(ctx context.Context, id string) (Track, error) {
	data, err := l.run(ctx, "get", id)
	var t Track
	if err == nil {
		err = json.Unmarshal(data, &t)
	}
	return t, err
}

// Open rejects symlinks escaping the configured root, stale paths and non-files.
func (l *Library) Open(t Track) (*os.File, error) {
	if !filepath.IsLocal(t.Path) {
		return nil, fmt.Errorf("invalid library path")
	}
	root, err := filepath.EvalSymlinks(l.Root)
	if err != nil {
		return nil, fmt.Errorf("USB drive unavailable: %w", err)
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, t.Path))
	if err != nil {
		return nil, fmt.Errorf("song unavailable; check USB drive: %w", err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("song is outside music directory")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("song is not a regular file")
	}
	return f, nil
}
func (l *Library) Art(ctx context.Context, id string) ([]byte, error) { return l.run(ctx, "art", id) }
func (l *Library) URL(id string) string                               { return l.BaseURL + "/music/" + id + ".mp3" }
func (l *Library) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/music/"), ".mp3")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	t, err := l.Get(ctx, id)
	cancel()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := l.Open(t)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	http.ServeContent(w, r, t.Title+".mp3", info.ModTime(), f)
}

func (l *Library) Album(ctx context.Context, id string) ([]Track, error) {
	data, err := l.run(ctx, "album", id)
	var tracks []Track
	if err == nil {
		err = json.Unmarshal(data, &tracks)
	}
	return tracks, err
}

// Mix returns every available indexed song once, in shuffled order.
func (l *Library) Mix(ctx context.Context) ([]Track, error) {
	data, err := l.run(ctx, "mix")
	if err != nil {
		return nil, err
	}
	var tracks []Track
	if err := json.Unmarshal(data, &tracks); err != nil {
		return nil, err
	}
	available := make([]Track, 0, len(tracks))
	for _, track := range tracks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, err := l.Open(track)
		if err != nil {
			continue
		}
		f.Close()
		available = append(available, track)
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("no available USB songs; check the drive and refresh the library")
	}
	return available, nil
}
