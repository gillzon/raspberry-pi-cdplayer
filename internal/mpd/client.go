// Package mpd implements the small part of MPD's line protocol needed by the appliance.
package mpd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	Address string
	Device  string
	// AutoDevice omits the device path to work around MPD releases whose CD
	// parser splits at the first slash. Use only with one audio CD drive.
	AutoDevice     bool
	conn           net.Conn
	reader         *bufio.Scanner
	dial           func(context.Context, string, string) (net.Conn, error)
	commandTimeout time.Duration // zero uses the normal five-second deadline
	requestedAt    time.Time
	lastStatus     map[string]string
}

// Connect returns true for a new session so the controller can restore the queue
// after MPD restarts. A successful ping preserves user pause/stop and album end.
func (c *Client) Connect(ctx context.Context) (bool, error) {
	if c.conn != nil {
		_, err := c.command("ping")
		return false, err
	}
	dial := c.dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 3 * time.Second}).DialContext
	}
	conn, err := dial(ctx, "tcp", c.Address)
	if err != nil {
		return false, fmt.Errorf("connect to MPD: %w", err)
	}
	c.conn, c.reader = conn, bufio.NewScanner(conn)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if !c.reader.Scan() {
		err := c.reader.Err()
		c.Close()
		if err != nil {
			return false, fmt.Errorf("read MPD greeting: %w", err)
		}
		return false, fmt.Errorf("MPD closed connection before greeting")
	}
	if !strings.HasPrefix(c.reader.Text(), "OK MPD ") {
		c.Close()
		return false, fmt.Errorf("invalid MPD greeting")
	}
	return true, nil
}

func (c *Client) Close() {
	c.lastStatus = nil
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) command(command string) (map[string]string, error) {
	return c.commandValues(command, nil)
}

func (c *Client) commandValues(command string, valueReceived func(string, string)) (map[string]string, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("MPD is disconnected")
	}
	timeout := c.commandTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	c.conn.SetDeadline(time.Now().Add(timeout))
	if _, err := fmt.Fprintln(c.conn, command); err != nil {
		c.Close()
		return nil, commandFailure(command, timeout, err)
	}
	values := make(map[string]string)
	for c.reader.Scan() {
		line := c.reader.Text()
		if line == "OK" {
			return values, nil
		}
		if strings.HasPrefix(line, "ACK ") {
			return nil, fmt.Errorf("MPD %s: %s", command, line)
		}
		if key, value, ok := strings.Cut(line, ": "); ok {
			values[key] = value
			if valueReceived != nil {
				valueReceived(key, value)
			}
		}
	}
	err := c.reader.Err()
	c.Close()
	if err == nil {
		err = fmt.Errorf("MPD closed the connection")
	}
	return nil, commandFailure(command, timeout, err)
}

func commandFailure(command string, timeout time.Duration, err error) error {
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return fmt.Errorf("MPD command %q timed out after %s; its outcome is unknown; the connection was closed for recovery: %w", command, timeout, err)
	}
	return fmt.Errorf("MPD command %q failed: %w", command, err)
}

func (c *Client) Clear() error {
	c.requestedAt = time.Time{}
	for _, command := range []string{"stop", "clear", "clearerror"} {
		if _, err := c.command(command); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) Start(tracks []int) error {
	if err := c.Clear(); err != nil {
		return err
	}
	for _, command := range []string{"random 0", "repeat 0", "single 0", "consume 0", "crossfade 0"} {
		if _, err := c.command(command); err != nil {
			return err
		}
	}
	for _, track := range tracks {
		uri := c.trackURI(track)
		if _, err := c.command("add " + strconv.Quote(uri)); err != nil {
			return err
		}
	}
	_, err := c.command("play 0")
	if err == nil {
		c.requestedAt = time.Now()
	}
	return err
}

func (c *Client) PlaybackError() error {
	status, err := c.command("status")
	if err != nil {
		return err
	}
	c.lastStatus = status
	if message := status["error"]; message != "" {
		return fmt.Errorf("MPD playback: %s", message)
	}
	if !c.requestedAt.IsZero() && status["state"] == "play" {
		elapsed, _ := strconv.ParseFloat(status["elapsed"], 64)
		if elapsed > 0 {
			slog.Info("MPD playback progressing", "since_request", time.Since(c.requestedAt), "elapsed_seconds", elapsed)
			c.requestedAt = time.Time{}
		}
	}
	return nil
}

// Status is used by the controller loop only; web handlers receive a copy.
func (c *Client) Status() map[string]string { return c.lastStatus }

func (c *Client) Control(action string, position int) error {
	var command string
	switch action {
	case "play":
		command = "play"
	case "pause":
		command = "pause 1"
	case "stop":
		command = "stop"
	case "next", "previous":
		command = action
	case "track":
		if position < 0 {
			return fmt.Errorf("invalid track position")
		}
		command = fmt.Sprintf("play %d", position)
	default:
		return fmt.Errorf("unknown playback action")
	}
	_, err := c.command(command)
	return err
}

func (c *Client) trackURI(track int) string {
	device := c.Device
	if c.AutoDevice {
		device = ""
	}
	return fmt.Sprintf("cdda://%s/%d", device, track)
}

// QueueMatches checks every queued URI in order, without changing playback.
// A socket reconnection alone does not mean MPD lost its queue or position.
func (c *Client) QueueMatches(tracks []int) (bool, error) {
	count := 0
	matches := true
	_, err := c.commandValues("playlistinfo", func(key, value string) {
		if key != "file" {
			return
		}
		if count >= len(tracks) || value != c.trackURI(tracks[count]) {
			matches = false
		}
		count++
	})
	return matches && count == len(tracks), err
}
