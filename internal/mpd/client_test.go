package mpd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func serve(t *testing.T, handler func(string) string) (*Client, <-chan []string) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close(); serverConn.Close() })
	done := make(chan []string, 1)
	go func() {
		conn := serverConn
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		fmt.Fprintln(conn, "OK MPD 0.23.0")
		var commands []string
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			cmd := scanner.Text()
			commands = append(commands, cmd)
			if response := handler(cmd); response != "" {
				fmt.Fprint(conn, response)
			}
		}
		done <- commands
	}()
	c := &Client{Device: "/dev/sr0", dial: func(context.Context, string, string) (net.Conn, error) {
		return clientConn, nil
	}}
	t.Cleanup(c.Close)
	return c, done
}

func TestQueueAndHealthProtocol(t *testing.T) {
	c, done := serve(t, func(cmd string) string {
		if cmd == "status" {
			return "state: stop\nOK\n"
		}
		return "OK\n"
	})
	if fresh, err := c.Connect(context.Background()); !fresh || err != nil {
		t.Fatalf("connect: %v %v", fresh, err)
	}
	if err := c.Start([]int{1, 2, 4}); err != nil {
		t.Fatal(err)
	}
	if fresh, err := c.Connect(context.Background()); fresh || err != nil {
		t.Fatalf("ping: %v %v", fresh, err)
	}
	if err := c.PlaybackError(); err != nil {
		t.Fatal(err)
	}
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	c.Close()
	want := []string{"stop", "clear", "clearerror", "random 0", "repeat 0", "single 0", "consume 0", "crossfade 0", `add "cdda:///dev/sr0/1"`, `add "cdda:///dev/sr0/2"`, `add "cdda:///dev/sr0/4"`, "play 0", "ping", "status", "stop", "clear", "clearerror"}
	if got := <-done; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands: %v; want %v", got, want)
	}
}

func TestACKDoesNotStartPartialQueue(t *testing.T) {
	c, done := serve(t, func(cmd string) string {
		if strings.HasPrefix(cmd, "add ") {
			return "ACK [50@0] {add} No such song\n"
		}
		return "OK\n"
	})
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Start([]int{1, 2}); err == nil || !strings.Contains(err.Error(), "No such song") {
		t.Fatalf("error: %v", err)
	}
	c.Close()
	for _, cmd := range <-done {
		if cmd == "play 0" {
			t.Fatal("played partial queue")
		}
	}
}

func TestAutoDeviceWorksWithFirstSlashParser(t *testing.T) {
	// MPD 0.24.5 splits the part after cdda:// at the FIRST slash, so an
	// explicit /dev/sr0/1 path is parsed as the invalid track "dev/sr0/1".
	// Exercise the generated commands against that parser behavior.
	var tracks []int
	c, done := serve(t, func(cmd string) string {
		if strings.HasPrefix(cmd, "add ") {
			uri, err := strconv.Unquote(strings.TrimPrefix(cmd, "add "))
			if err != nil {
				return "ACK [2@0] {add} Invalid argument\n"
			}
			device, track, found := strings.Cut(strings.TrimPrefix(uri, "cdda://"), "/")
			number, err := strconv.Atoi(track)
			if !found || device != "" || err != nil || number < 1 || number > 99 {
				return "ACK [2@0] {add} Bad track number\n"
			}
			tracks = append(tracks, number)
		}
		return "OK\n"
	})
	c.AutoDevice = true
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Start([]int{1, 2, 12}); err != nil {
		t.Fatal(err)
	}
	c.Close()
	<-done
	if !reflect.DeepEqual(tracks, []int{1, 2, 12}) {
		t.Fatalf("tracks: %v", tracks)
	}
}

func TestAsynchronousPlaybackError(t *testing.T) {
	c, _ := serve(t, func(string) string { return "state: stop\nerror: Failed to open audio output\nOK\n" })
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.PlaybackError(); err == nil || !strings.Contains(err.Error(), "Failed to open audio output") {
		t.Fatalf("error: %v", err)
	}
}

func TestReconnectAfterDisconnect(t *testing.T) {
	attempts := 0
	c := &Client{dial: func(context.Context, string, string) (net.Conn, error) {
		attempts++
		attempt := attempts
		client, server := net.Pipe()
		t.Cleanup(func() { client.Close(); server.Close() })
		go func() {
			defer server.Close()
			server.SetDeadline(time.Now().Add(10 * time.Second))
			fmt.Fprintln(server, "OK MPD 0.23.0")
			if attempt == 1 {
				return
			}
			scanner := bufio.NewScanner(server)
			for scanner.Scan() {
				fmt.Fprintln(server, "OK")
			}
		}()
		return client, nil
	}}
	defer c.Close()
	if fresh, err := c.Connect(context.Background()); !fresh || err != nil {
		t.Fatalf("initial connect: %v %v", fresh, err)
	}
	if _, err := c.Connect(context.Background()); err == nil {
		t.Fatal("missing disconnect error")
	}
	if fresh, err := c.Connect(context.Background()); !fresh || err != nil {
		t.Fatalf("reconnect: %v %v", fresh, err)
	}
	if fresh, err := c.Connect(context.Background()); fresh || err != nil {
		t.Fatalf("ping: %v %v", fresh, err)
	}
	if attempts != 2 {
		t.Fatalf("connection attempts: %d", attempts)
	}
}

func TestPlaybackControls(t *testing.T) {
	c, done := serve(t, func(string) string { return "OK\n" })
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"play", "pause", "stop", "next", "previous", "track"} {
		if err := c.Control(action, 2); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Control("track", -1); err == nil {
		t.Fatal("negative position accepted")
	}
	if err := c.Control("clear", 0); err == nil {
		t.Fatal("unlisted action accepted")
	}
	c.Close()
	if got, want := <-done, []string{"play", "pause 1", "stop", "next", "previous", "play 2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands: %v", got)
	}
}

func TestTimeoutNamesCommandAndDiscardsConnection(t *testing.T) {
	c, done := serve(t, func(string) string { return "" }) // accept command, never acknowledge
	c.commandTimeout = 20 * time.Millisecond
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := c.Control("next", 0)
	if err == nil || !strings.Contains(err.Error(), `MPD command "next" timed out`) {
		t.Fatalf("error: %v", err)
	}
	var networkError net.Error
	if !errors.As(err, &networkError) || !networkError.Timeout() {
		t.Fatalf("lost timeout cause: %v", err)
	}
	if c.conn != nil {
		t.Fatal("kept timed-out socket; late reply could corrupt next command")
	}
	if got := <-done; !reflect.DeepEqual(got, []string{"next"}) {
		t.Fatalf("unsafe command replay: %v", got)
	}
}

func TestQueueMatchesChecksAllURIsInOrder(t *testing.T) {
	for _, tt := range []struct {
		name, response string
		want           bool
	}{
		{"matching", "file: cdda:///1\nPos: 0\nfile: cdda:///3\nPos: 1\nOK\n", true},
		{"reordered", "file: cdda:///3\nfile: cdda:///1\nOK\n", false},
		{"missing", "file: cdda:///1\nOK\n", false},
		{"extra", "file: cdda:///1\nfile: cdda:///3\nfile: cdda:///4\nOK\n", false},
		{"empty", "OK\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, done := serve(t, func(string) string { return tt.response })
			c.AutoDevice = true
			if _, err := c.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, err := c.QueueMatches([]int{1, 3})
			if err != nil || got != tt.want {
				t.Fatalf("match: %v %v", got, err)
			}
			c.Close()
			if commands := <-done; !reflect.DeepEqual(commands, []string{"playlistinfo"}) {
				t.Fatalf("modified queue: %v", commands)
			}
		})
	}
}

func TestTrackAcknowledgmentUpdatesNavigationWithoutStatusRoundTrip(t *testing.T) {
	c, done := serve(t, func(string) string { return "OK\n" })
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.lastStatus = map[string]string{"song": "0", "state": "play", "elapsed": "20", "duration": "180"}
	if err := c.Control("track", 3); err != nil {
		t.Fatal(err)
	}
	status := c.Status()
	if status["song"] != "3" || status["state"] != "play" || status["elapsed"] != "0" || status["duration"] != "" {
		t.Fatalf("stale navigation state: %v", status)
	}
	c.Close()
	if got := <-done; !reflect.DeepEqual(got, []string{"play 3"}) {
		t.Fatalf("unexpected extra MPD requests: %v", got)
	}
}
func TestRejectedTrackDoesNotMoveDisplayedPosition(t *testing.T) {
	c, _ := serve(t, func(string) string { return "ACK [2@0] {play} Bad song index\n" })
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.lastStatus = map[string]string{"song": "0", "state": "pause"}
	if err := c.Control("track", 3); err == nil {
		t.Fatal("missing rejection")
	}
	if c.Status()["song"] != "0" || c.Status()["state"] != "pause" {
		t.Fatal("rejected track changed displayed state")
	}
}

func TestSlowTrackChangeAcknowledgedWithoutReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("exercises a response beyond the former five-second deadline")
	}
	c, done := serve(t, func(command string) string {
		if command == "play 1" {
			time.Sleep(6 * time.Second)
		}
		return "OK\n"
	})
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Control("track", 1); err != nil {
		t.Fatalf("slow but successful track change failed: %v", err)
	}
	if c.Status()["song"] != "1" {
		t.Fatal("acknowledged track not reflected")
	}
	c.Close()
	if got := <-done; !reflect.DeepEqual(got, []string{"play 1"}) {
		t.Fatalf("track command repeated: %v", got)
	}
}

func TestCachedAudioURLsReplaceCDDAQueue(t *testing.T) {
	c, done := serve(t, func(string) string { return "OK\n" })
	c.TrackURL = func(track int) string { return fmt.Sprintf("http://127.0.0.1:1234/audio/session/%d.wav", track) }
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Start([]int{1, 3}); err != nil {
		t.Fatal(err)
	}
	c.Close()
	commands := <-done
	var added []string
	for _, command := range commands {
		if strings.HasPrefix(command, "add ") {
			added = append(added, command)
		}
	}
	if want := []string{`add "http://127.0.0.1:1234/audio/session/1.wav"`, `add "http://127.0.0.1:1234/audio/session/3.wav"`}; !reflect.DeepEqual(added, want) {
		t.Fatalf("URLs: %v", added)
	}
}

func TestUSBAlbumStartsSelectedSong(t *testing.T) {
	c, done := serve(t, func(string) string { return "OK\n" })
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.StartURLs([]string{"http://127.0.0.1:123/music/a.mp3", "http://127.0.0.1:123/music/b.mp3"}, 1); err != nil {
		t.Fatal(err)
	}
	if c.Status()["song"] != "1" || c.Status()["state"] != "play" {
		t.Fatal("selected song not reflected in status")
	}
	c.Close()
	cmds := <-done
	if cmds[len(cmds)-1] != "play 1" {
		t.Fatalf("commands: %v", cmds)
	}
}

func TestLargeUSBMixUsesBoundedBatchesAndStartsAfterEverySong(t *testing.T) {
	inList, added, batches := false, 0, 0
	c, done := serve(t, func(cmd string) string {
		switch {
		case cmd == "command_list_begin":
			inList = true
			batches++
			return ""
		case cmd == "command_list_end":
			inList = false
			return "OK\n"
		case strings.HasPrefix(cmd, "add "):
			added++
			if inList {
				return ""
			}
		case cmd == "play 0":
			if added != 27362 {
				t.Errorf("started before full mix: %d songs", added)
			}
		}
		return "OK\n"
	})
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	urls := make([]string, 27362)
	for i := range urls {
		urls[i] = fmt.Sprintf("http://127.0.0.1:123/music/%064x.mp3", i)
	}
	if err := c.StartURLs(urls, 0); err != nil {
		t.Fatal(err)
	}
	c.Close()
	commands := <-done
	if batches != 107 || commands[len(commands)-1] != "play 0" {
		t.Fatalf("batches=%d, last command=%s", batches, commands[len(commands)-1])
	}
}

func TestFailedBatchNeverPlaysPartialMix(t *testing.T) {
	inList := false
	c, done := serve(t, func(cmd string) string {
		if cmd == "command_list_begin" {
			inList = true
			return ""
		}
		if cmd == "command_list_end" {
			inList = false
			return "ACK [51@0] {add} playlist is too large\n"
		}
		if inList {
			return ""
		}
		return "OK\n"
	})
	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	urls := make([]string, 257)
	for i := range urls {
		urls[i] = "http://localhost/music.mp3"
	}
	if err := c.StartURLs(urls, 0); err == nil || !strings.Contains(err.Error(), "queue limit") {
		t.Fatalf("unexpected result: %v", err)
	}
	c.Close()
	for _, cmd := range <-done {
		if strings.HasPrefix(cmd, "play ") {
			t.Fatal("partial mix played")
		}
	}
}
