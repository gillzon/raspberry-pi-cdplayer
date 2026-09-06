package player

import (
	"context"
	"log/slog"

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
	Drive   Drive
	Backend Backend
	current string
	ready   bool
}

// Step is called immediately at startup and periodically thereafter. Failed
// reads preserve the current session; failed queue changes are retried.
func (c *Controller) Step(ctx context.Context) error {
	d, err := c.Drive.Read()
	if err != nil {
		return err
	}
	fresh, err := c.Backend.Connect(ctx)
	if err != nil {
		return err
	}
	if fresh {
		c.ready = false
	}
	if !c.ready || d.ID != c.current {
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
			slog.Info("CD playback requested", "tracks", len(d.Tracks))
		}
	}
	return c.Backend.PlaybackError()
}
