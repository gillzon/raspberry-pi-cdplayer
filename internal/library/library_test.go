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
)

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
