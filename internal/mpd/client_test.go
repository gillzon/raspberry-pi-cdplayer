package mpd

import (
	"bufio"
	"context"
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
			fmt.Fprint(conn, handler(cmd))
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
