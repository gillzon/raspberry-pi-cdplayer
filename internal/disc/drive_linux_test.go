//go:build linux && (amd64 || arm64 || arm || 386)

package disc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMissingDriveIsAbsent(t *testing.T) {
	d, err := (&Drive{Device: filepath.Join(t.TempDir(), "missing")}).Read()
	if err != nil || d.ID != "" {
		t.Fatalf("missing drive: %+v, %v", d, err)
	}
}

func TestNonDriveIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Drive{Device: path}).Read(); err == nil {
		t.Fatal("regular file accepted as drive")
	}
}

func TestAudioLayoutCDExtra(t *testing.T) {
	// The reported disc: track 13 begins at 247511, followed by a data
	// session at 294071. Its audio ends at 282671, before the session gap.
	var offsets [100]uint32
	offsets[1], offsets[13], offsets[14], offsets[0] = 150, 247511+150, 294071+150, 320000+150
	for _, tc := range []struct {
		name    string
		tracks  []int
		session int
		wantEnd int
	}{
		{"CD Extra", []int{13}, 294071, 282671},
		{"single session mixed mode", []int{13}, 0, 294071},
		{"unavailable session", []int{13}, -1, 294071},
		{"next track is audio", []int{13, 14}, 294071, 294071},
		{"session gap outside track", []int{13}, 250000, 294071},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := audioLayout(tc.tracks, 1, 14, offsets, tc.session)
			if got[0] != (TrackLayout{Number: 13, Start: 247511, End: tc.wantEnd}) {
				t.Fatalf("track 13: %+v", got[0])
			}
		})
	}
}

func TestAudioLayoutNormalDiscAndTrack99(t *testing.T) {
	var offsets [100]uint32
	offsets[1], offsets[2], offsets[0] = 150, 15150, 30150
	got := audioLayout([]int{1, 2}, 1, 2, offsets, 0)
	if got[0] != (TrackLayout{1, 0, 15000}) || got[1] != (TrackLayout{2, 15000, 30000}) {
		t.Fatalf("ordinary CD changed: %+v", got)
	}
	offsets[99] = 15150
	got = audioLayout([]int{99}, 99, 99, offsets, 0)
	if got[0] != (TrackLayout{99, 15000, 30000}) {
		t.Fatalf("track 99: %+v", got)
	}
}
