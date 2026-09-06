// Package mpd implements the small part of MPD's line protocol needed by the appliance.
package mpd

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	Address string
	Device  string
	conn    net.Conn
	reader  *bufio.Scanner
	dial    func(context.Context, string, string) (net.Conn, error)
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
	if !c.reader.Scan() || !strings.HasPrefix(c.reader.Text(), "OK MPD ") {
		c.Close()
		return false, fmt.Errorf("invalid MPD greeting")
	}
	return true, nil
}

func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) command(command string) (map[string]string, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("MPD is disconnected")
	}
	c.conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintln(c.conn, command); err != nil {
		c.Close()
		return nil, err
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
		}
	}
	err := c.reader.Err()
	c.Close()
	if err == nil {
		err = fmt.Errorf("MPD closed the connection")
	}
	return nil, err
}

func (c *Client) Clear() error {
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
		uri := fmt.Sprintf("cdda://%s/%d", c.Device, track)
		if _, err := c.command("add " + strconv.Quote(uri)); err != nil {
			return err
		}
	}
	_, err := c.command("play 0")
	return err
}

func (c *Client) PlaybackError() error {
	status, err := c.command("status")
	if err != nil {
		return err
	}
	if message := status["error"]; message != "" {
		return fmt.Errorf("MPD playback: %s", message)
	}
	return nil
}
