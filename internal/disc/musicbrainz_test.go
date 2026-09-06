package disc

import "testing"

func TestMusicBrainzPublishedExample(t *testing.T) {
	// https://musicbrainz.org/doc/Disc_ID_Calculation
	offsets := [100]uint32{95462, 150, 15363, 32314, 46592, 63414, 80489}
	if got := musicBrainzID(1, 6, offsets); got != "49HHV7Eb8UKF3aQiNmu1GR8vKTY-" {
		t.Fatalf("disc ID: %s", got)
	}
}
