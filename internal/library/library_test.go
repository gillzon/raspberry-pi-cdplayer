package library

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStreamingScanPublishesFinalProgress(t *testing.T) {
	if err := exec.Command("python3", "-c", "import mutagen, sqlite3").Run(); err != nil {
		t.Skip("requires python3 and Mutagen")
	}
	root := t.TempDir()
	l, err := New(root, filepath.Join(t.TempDir(), "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.mp3", "two.mp3"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("music"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := l.runScan(ctx); err != nil {
		t.Fatal(err)
	}
	status := l.Snapshot()
	if status.Phase != "complete" || status.Count != 2 || status.Processed != 2 || status.Total != 2 || status.Percent != 100 || status.Checkpointed != 2 {
		t.Fatalf("bad progress: %+v", status)
	}
	result, err := l.Search(ctx, "", 0)
	if err != nil || result.Total != 2 || result.Status.Percent != 100 {
		t.Fatalf("search: %+v %v", result, err)
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err := l.runScan(canceled); err == nil {
		t.Fatal("canceled scan succeeded")
	}
}

func TestRunUsesSavedLibraryUntilExplicitRefresh(t *testing.T) {
	if err := exec.Command("python3", "-c", "import mutagen, sqlite3").Run(); err != nil {
		t.Skip("requires python3 and Mutagen")
	}
	root := t.TempDir()
	database := filepath.Join(t.TempDir(), "library.sqlite")
	if err := os.WriteFile(filepath.Join(root, "one.mp3"), []byte("music"), 0600); err != nil {
		t.Fatal(err)
	}
	start := func() (*Library, func()) {
		l, err := New(root, database)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); l.Run(ctx) }()
		stop := func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("library did not stop")
			}
		}
		t.Cleanup(stop)
		return l, stop
	}
	wait := func(l *Library, phase string, count int) {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			s := l.Snapshot()
			if !s.Scanning && s.Phase == phase && s.Count == count {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("wanted %s/%d, got %+v", phase, count, l.Snapshot())
	}
	first, stop := start()
	wait(first, "complete", 1) // The first empty database still scans automatically.
	stop()
	// A reboot with the USB disk unavailable must still load the cached library.
	if err := os.Rename(root, root+"-offline"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(root+"-offline", root)
	rebooted, _ := start()
	wait(rebooted, "cached", 1)
	result, err := rebooted.Search(context.Background(), "", 0)
	if err != nil || result.Total != 1 {
		t.Fatalf("cached search: %+v %v", result, err)
	}
	if err := os.Rename(root+"-offline", root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "two.mp3"), []byte("music"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = rebooted.Search(context.Background(), "", 0)
	if err != nil || result.Total != 1 {
		t.Fatalf("unexpected automatic refresh: %+v %v", result, err)
	}
	rebooted.Refresh()
	wait(rebooted, "complete", 2)
}

func TestOpenRejectsEscapesAndRemovedFiles(t *testing.T) {
	root := t.TempDir()
	l := &Library{Root: root}
	outside := filepath.Join(t.TempDir(), "secret.mp3")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.mp3")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../secret.mp3", outside, "link.mp3", "missing.mp3", "."} {
		if f, err := l.Open(Track{Path: path}); err == nil {
			f.Close()
			t.Fatalf("opened %s", path)
		}
	}
	path := filepath.Join(root, "song.mp3")
	if err := os.WriteFile(path, []byte("music"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := l.Open(Track{Path: "song.mp3"})
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if f, err := l.Open(Track{Path: "song.mp3"}); err == nil {
		f.Close()
		t.Fatal("opened removed song")
	}
}

func TestIndexSearchAndRangeStreaming(t *testing.T) {
	if err := exec.Command("python3", "-c", "import mutagen, sqlite3").Run(); err != nil {
		t.Skip("requires python3 and Mutagen")
	}
	root := t.TempDir()
	l, err := New(root, filepath.Join(t.TempDir(), "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "My Song.mp3"), []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.run(context.Background(), "scan"); err != nil {
		t.Fatal(err)
	}
	result, err := l.Search(context.Background(), "my song", 0)
	if err != nil || result.Total != 1 {
		t.Fatalf("search: %+v %v", result, err)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "path") {
		t.Fatal("search exposed path")
	}
	id := result.Tracks[0].ID
	req := httptest.NewRequest("GET", "/music/"+id+".mp3", nil)
	req.Header.Set("Range", "bytes=2-5")
	w := httptest.NewRecorder()
	l.ServeHTTP(w, req)
	if w.Code != 206 || w.Body.String() != "2345" {
		t.Fatalf("range: %d %s", w.Code, w.Body.String())
	}
	if err := os.Remove(filepath.Join(root, "My Song.mp3")); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	l.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("removed file: %d", w.Code)
	}
}

func TestMixSkipsMissingSongsAndReportsEmptyLibrary(t *testing.T) {
	if err := exec.Command("python3", "-c", "import mutagen, sqlite3").Run(); err != nil {
		t.Skip("requires python3 and Mutagen")
	}
	root := t.TempDir()
	l, err := New(root, filepath.Join(t.TempDir(), "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := l.Mix(ctx); err == nil {
		t.Fatal("empty library accepted")
	}
	for _, name := range []string{"one.mp3", "two.mp3", "three.mp3"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("music"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.run(ctx, "scan"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "two.mp3")); err != nil {
		t.Fatal(err)
	}
	tracks, err := l.Mix(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 || tracks[0].ID == tracks[1].ID {
		t.Fatalf("wrong mix: %+v", tracks)
	}
	for _, track := range tracks {
		if track.Path == "two.mp3" {
			t.Fatal("queued missing song")
		}
	}
}
