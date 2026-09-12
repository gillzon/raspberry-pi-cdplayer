package disc

import "fmt"

// Cache the TOC while the drive reports the same ready session. Re-reading every
// track on every poll competes with MPD's digital audio extraction on USB drives.
type tocCache struct {
	disc  Disc
	valid bool
}

func (c *tocCache) read(status int, load func() (Disc, error)) (Disc, error) {
	return c.readMedia(status, false, load)
}

func (c *tocCache) readMedia(status int, changed bool, load func() (Disc, error)) (Disc, error) {
	if changed {
		*c = tocCache{}
	}
	switch status {
	case 1, 2, 3: // CDS_NO_DISC, CDS_TRAY_OPEN, CDS_DRIVE_NOT_READY
		// A physical eject can report not-ready while the tray is moving.
		// Publish no playable disc so cached playback stops during that transition.
		*c = tocCache{}
		return Disc{}, nil
	case 4: // CDS_DISC_OK
		if c.valid {
			return c.disc, nil
		}
		d, err := load()
		if err != nil {
			return Disc{}, err
		}
		c.disc, c.valid = d, true
		return d, nil
	default:
		// An unknown status may hide a disc swap. Reload once ready again.
		*c = tocCache{}
		return Disc{}, fmt.Errorf("drive not ready (status %d)", status)
	}
}
