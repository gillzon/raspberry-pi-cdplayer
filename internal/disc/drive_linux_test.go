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
