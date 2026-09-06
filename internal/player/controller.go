package player

import (
	"context"
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
	Drive     Drive
	Backend   Backend
	current   string
	ready     bool
	observed  disc.Disc
	ejectedID string
}

// Step is called immediately at startup and periodically thereafter. Failed
// reads preserve the current session; failed queue changes are retried.
func (c *Controller) Step(ctx context.Context) error {
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
	fresh, err := c.Backend.Connect(ctx)
	if err != nil {
		return err
	}
	if fresh {
		c.ready = false
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
