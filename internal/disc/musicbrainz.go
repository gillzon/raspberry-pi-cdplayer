package disc

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strings"
)

// musicBrainzID uses absolute CD frame offsets (MSF already includes the
// 150-frame lead-in). Slot zero is lead-out; unused track slots remain zero.
func musicBrainzID(first, last int, offsets [100]uint32) string {
	var text strings.Builder
	fmt.Fprintf(&text, "%02X%02X", first, last)
	for _, offset := range offsets {
		fmt.Fprintf(&text, "%08X", offset)
	}
	hash := sha1.Sum([]byte(text.String()))
	return base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._").WithPadding('-').EncodeToString(hash[:])
}

// metadataDiscID supports audio CDs and verified CD Extra layouts. MusicBrainz
// excludes the data track and the 11400-frame inter-session gap from the ID.
// https://musicbrainz.org/doc/Disc_ID_Calculation
func metadataDiscID(first, last int, offsets [100]uint32, layout []TrackLayout) string {
	if len(layout) == 0 {
		return ""
	}
	for i, track := range layout {
		if track.Number != first+i {
			return "" // Other mixed-mode layouts need separate handling.
		}
	}
	audioLast := layout[len(layout)-1]
	if audioLast.Number == last {
		return musicBrainzID(first, last, offsets)
	}
	if audioLast.Number != last-1 || audioLast.End <= audioLast.Start ||
		audioLast.End != int(offsets[last])-150-11400 {
		return "" // Only accept a session boundary already verified by the probe.
	}
	offsets[0] = uint32(audioLast.End + 150)
	for n := audioLast.Number + 1; n < len(offsets); n++ {
		offsets[n] = 0
	}
	return musicBrainzID(first, audioLast.Number, offsets)
}
