package disc

import "fmt"

// Cache the TOC while the drive reports the same ready session. Re-reading every
// track on every poll competes with MPD's digital audio extraction on USB drives.
type tocCache struct {
	disc  Disc
	valid bool
}

func (c *tocCache) read(status int, load func() (Disc, error)) (Disc, error) {
	switch status {
	case 1, 2: // CDS_NO_DISC, CDS_TRAY_OPEN
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
		// A not-ready interval may hide a disc swap. Reload once ready again.
		*c = tocCache{}
		return Disc{}, fmt.Errorf("drive not ready (status %d)", status)
	}
}
