package audio

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/mpd"
)

type pacedTestReader struct{ ctx context.Context }

func (r pacedTestReader) Read(_ int, count int) ([]byte, error) {
	timer := time.NewTimer(time.Duration(count) * time.Second / 150) // 2x CD rate
	defer timer.Stop()
	select {
	case <-timer.C:
		return make([]byte, count*sectorBytes), nil
	case <-r.ctx.Done():
		return nil, r.ctx.Err()
	}
}
func (r pacedTestReader) Close() error { return nil }

// Exercises a real MPD decoder and HTTP buffering, without a physical drive or
// audio device. An optional check on hosts with MPD installed and loopback access.
func TestRealMPDStartsAndChangesCachedTracks(t *testing.T) {
	binary, err := exec.LookPath("mpd")
	if err != nil {
		t.Skip("MPD not installed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback sockets unavailable: %v", err)
	}
	address := listener.Addr().String()
	_, port, _ := net.SplitHostPort(address)
	listener.Close()
	root := t.TempDir()
	config := fmt.Sprintf("music_directory %q\nbind_to_address \"127.0.0.1\"\nport %q\nrestore_paused \"yes\"\ninput {\n plugin \"cdio_paranoia\"\n enabled \"no\"\n}\naudio_output {\n type \"null\"\n name \"Test\"\n}\n", root, port)
	path := filepath.Join(root, "mpd.conf")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(root, "mpd.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	command := exec.Command(binary, "--no-daemon", path)
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		command.Process.Kill()
		command.Wait()
		if t.Failed() {
			log, _ := os.ReadFile(logFile.Name())
			t.Log(string(log))
		}
	}()
	client := &mpd.Client{Address: address}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		if _, err = client.Connect(ctx); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	wake := make(chan struct{}, 1)
	cache := &Cache{Root: filepath.Join(root, "cache"), ReadyNotify: wake,
		Open: func(ctx context.Context, _ string, _ []Layout) (Reader, error) { return pacedTestReader{ctx}, nil }}
	server := httptest.NewServer(cache)
	defer server.Close()
	defer cache.Shutdown()
	cache.BaseURL = server.URL
	client.TrackURL = cache.TrackURL
	if err := cache.Observe("disc", "fake", []Layout{{1, 0, 9000}, {3, 9000, 18000}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	case <-ctx.Done():
		t.Fatal("no first audio")
	}
	waitPlaying := func(song string, started time.Time) {
		t.Helper()
		for time.Since(started) < 5*time.Second {
			if err := client.PlaybackError(); err != nil {
				t.Fatal(err)
			}
			status := client.Status()
			elapsed, _ := strconv.ParseFloat(status["elapsed"], 64)
			if status["song"] == song && status["state"] == "play" && elapsed > 0 {
				t.Logf("track position %s playing after %s", song, time.Since(started))
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("track %s did not start within 5s; status=%v", song, client.Status())
	}
	started := time.Now()
	if err := client.Start([]int{1, 3}); err != nil {
		t.Fatal(err)
	}
	waitPlaying("0", started)
	started = time.Now()
	cache.Select(3) // The hardware navigation path must do this before MPD play.
	if err := client.Control("track", 1); err != nil {
		t.Fatal(err)
	}
	waitPlaying("1", started)
	for _, action := range []string{"pause", "stop"} {
		if err := client.Control(action, 0); err != nil {
			t.Fatal(err)
		}
		cache.SetPaused(true)
		// Allow the single in-flight 100ms read to finish. MPD can retain
		// HTTP read-ahead demand, but it must not wake the optical reader.
		time.Sleep(200 * time.Millisecond)
		cached := cache.Status().CachedBytes
		time.Sleep(250 * time.Millisecond)
		if got := cache.Status().CachedBytes; got != cached {
			t.Fatalf("%s kept caching: %d -> %d", action, cached, got)
		}
		started = time.Now()
		cache.SetPaused(false)
		if err := client.Control("play", 0); err != nil {
			t.Fatal(err)
		}
		waitPlaying("1", started)
		deadline := time.Now().Add(time.Second)
		for cache.Status().CachedBytes == cached && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if cache.Status().CachedBytes <= cached {
			t.Fatalf("cache did not resume after %s", action)
		}
	}
}
