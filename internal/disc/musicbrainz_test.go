package disc

import "testing"

func TestMusicBrainzPublishedExample(t *testing.T) {
	// https://musicbrainz.org/doc/Disc_ID_Calculation
	offsets := [100]uint32{95462, 150, 15363, 32314, 46592, 63414, 80489}
	if got := musicBrainzID(1, 6, offsets); got != "49HHV7Eb8UKF3aQiNmu1GR8vKTY-" {
		t.Fatalf("disc ID: %s", got)
	}
}

func TestMusicBrainzCDExtraPublishedLayout(t *testing.T) {
	// Raw TOC from MusicBrainz's CD Extra example; the final track is data.
	offsets := [100]uint32{188333 + 150, 150, 14109, 33586, 53077, 65781, 77892, 99174, 125824 + 150}
	var layout []TrackLayout
	for n := 1; n <= 7; n++ {
		end := int(offsets[n+1]) - 150
		if n == 7 {
			end -= 11400
		}
		layout = append(layout, TrackLayout{Number: n, Start: int(offsets[n]) - 150, End: end})
	}
	if got := metadataDiscID(1, 8, offsets, layout); got != "BPnh1KU.hea1C.KMYWLGZkHJr0w-" {
		t.Fatalf("CD Extra ID=%s", got)
	}
	layout[6].End += 11400
	if got := metadataDiscID(1, 8, offsets, layout); got != "" {
		t.Fatal("unverified session accepted")
	}
	if got := metadataDiscID(1, 8, offsets, layout[1:]); got != "" {
		t.Fatal("leading data track accepted")
	}
}

func TestMetadataDiscIDNormalAudio(t *testing.T) {
	offsets := [100]uint32{95462, 150, 15363, 32314, 46592, 63414, 80489}
	var layout []TrackLayout
	for n := 1; n <= 6; n++ {
		layout = append(layout, TrackLayout{Number: n})
	}
	if got := metadataDiscID(1, 6, offsets, layout); got != "49HHV7Eb8UKF3aQiNmu1GR8vKTY-" {
		t.Fatalf("audio ID changed: %s", got)
	}
	if got := metadataDiscID(1, 6, offsets, nil); got != "" {
		t.Fatal("data-only disc accepted")
	}
}
