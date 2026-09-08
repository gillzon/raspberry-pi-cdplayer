package player

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
)

type Drive interface{ Read() (disc.Disc, error) }

type Backend interface {
	Connect(context.Context) (bool, error)
	Start([]int) error
	Clear() error
	PlaybackError() error
}

type Controller struct {
	Drive          Drive
	Backend        Backend
	Prepare        func(disc.Disc) error
	Release        func()
	current        string
	ready          bool
	observed       disc.Disc
	ejectedID      string
	usb            bool
	spotify        bool
	spotifyStopped bool
	queueCheck     bool
	spotifyIdle    bool
}

// Step is called immediately at startup and periodically thereafter. Failed
// reads preserve the current session; failed queue changes are retried.
func (c *Controller) Step(ctx context.Context) error {
	if c.usb {
		if _, err := c.Backend.Connect(ctx); err != nil {
			return err
		}
		return c.Backend.PlaybackError()
	}
	if c.spotify {
		fresh, err := c.Backend.Connect(ctx)
		if err != nil {
			return err
		}
		if fresh {
			c.spotifyStopped = false
		}
		return c.stopForSpotify()
	}
	probeStart := time.Now()
	d, err := c.Drive.Read()
	probeDuration := time.Since(probeStart)
	if probeDuration >= time.Second {
		slog.Warn("slow CD drive probe", "duration", probeDuration)
	}
	if err != nil {
		return err
	}
	c.observed = d
	if c.ejectedID != "" {
		if d.ID == c.ejectedID {
			// Some drives briefly report the old TOC while the tray is opening.
			c.observed = disc.Disc{}
			return nil
		}
		c.ejectedID = ""
	}
	if c.Prepare != nil {
		if err := c.Prepare(d); err != nil {
			if c.ready {
				// Stop a playlist whose audio source failed. Retain ready on a
				// failed clear so the next Step retries releasing MPD.
				if clearErr := c.Backend.Clear(); clearErr != nil {
					return errors.Join(err, clearErr)
				}
				c.ready = false
			}
			return err
		}
	}
	fresh, err := c.Backend.Connect(ctx)
	if err != nil {
		return err
	}
	if fresh {
		c.queueCheck = true
	}
	if c.queueCheck {
		matches := false
		if checker, ok := c.Backend.(interface{ QueueMatches([]int) (bool, error) }); ok && c.ready && d.ID == c.current {
			matches, err = checker.QueueMatches(d.Tracks)
			if err != nil {
				return err
			}
		}
		c.ready = matches
		c.queueCheck = false
	}
	if !c.ready || d.ID != c.current {
		queueStart := time.Now()
		if len(d.Tracks) == 0 {
			err = c.Backend.Clear()
		} else {
			err = c.Backend.Start(d.Tracks)
		}
		if err != nil {
			return err
		}
		c.current, c.ready = d.ID, true
		switch {
		case d.ID == "":
			slog.Info("waiting for an audio CD")
		case len(d.Tracks) == 0:
			slog.Info("disc has no audio tracks; ignoring")
		default:
			slog.Info("CD playback requested", "tracks", len(d.Tracks), "probe_duration", probeDuration, "queue_duration", time.Since(queueStart))
		}
	}
	return c.Backend.PlaybackError()
}

func (c *Controller) Disc() disc.Disc { return c.observed }

func (c *Controller) Eject() error {
	drive, ok := c.Drive.(interface{ Eject() error })
	if !ok {
		return fmt.Errorf("drive does not support eject")
	}
	if c.usb {
		return drive.Eject()
	}
	if c.Release != nil {
		c.Release()
		c.ready = false
	}
	if err := c.Backend.Clear(); err != nil {
		return fmt.Errorf("stop playback before eject: %w", err)
	}
	if err := drive.Eject(); err != nil {
		return err
	}
	c.ejectedID = c.observed.ID
	c.current, c.ready, c.observed = "", true, disc.Disc{}
	return nil
}

// UseSpotify suppresses autoplay even if stopping MPD fails. The caller must
// keep the Spotify sink gated until this returns successfully.
func (c *Controller) UseSpotify(ctx context.Context) error {
	c.usb = false
	c.spotify = true
	c.spotifyIdle = false
	if c.Release != nil {
		c.Release()
		c.ready = false
	}
	fresh, err := c.Backend.Connect(ctx)
	if err != nil {
		return err
	}
	if fresh {
		c.spotifyStopped = false
	}
	return c.stopForSpotify()
}
func (c *Controller) stopForSpotify() error {
	if c.spotifyStopped {
		return nil
	}
	if err := c.Backend.Clear(); err != nil {
		return err
	}
	c.spotifyStopped = true
	if c.Release != nil {
		c.Release()
	}
	slog.Info("CD stopped for Spotify; manual CD start required to return")
	return nil
}
func (c *Controller) UseCD() {
	c.usb = false
	c.spotify = false
	c.spotifyStopped = false
	c.ready = false
}

// UseUSB suspends CD autoplay until the user explicitly selects CD again.
func (c *Controller) UseUSB() {
	c.usb = true
	c.spotify = false
	c.spotifyStopped = false
	c.ready = false
	if c.Release != nil {
		c.Release()
	}
}
func (c *Controller) Source() string {
	if c.usb {
		return "usb"
	}
	if c.spotify {
		if c.spotifyIdle {
			return "idle"
		}
		return "spotify"
	}
	return "cd"
}

// SpotifyDisconnected exposes manual CD start without enabling autoplay.
func (c *Controller) SpotifyDisconnected() {
	if c.spotify {
		c.spotifyIdle = true
	}
}
