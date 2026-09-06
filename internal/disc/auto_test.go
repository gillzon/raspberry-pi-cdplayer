package disc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpticalDiscovery(t *testing.T) {
	root := t.TempDir()
	sys := filepath.Join(root, "sys")
	dev := filepath.Join(root, "dev")
	os.MkdirAll(sys, 0700)
	os.MkdirAll(dev, 0700)
	add := func(name, kind string) {
		t.Helper()
		p := filepath.Join(sys, name, "device")
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "type"), []byte(kind), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dev, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want string) {
		t.Helper()
		got, err := findOpticalDrive(sys, dev)
		if err != nil || got != want {
			t.Fatalf("got %q, %v; want %q", got, err, want)
		}
	}
	add("sda", "0\n")
	check("")
	add("sr0", "5\n")
	check(filepath.Join(dev, "sr0"))
	os.RemoveAll(filepath.Join(sys, "sr0"))
	os.Remove(filepath.Join(dev, "sr0"))
	check("")
	add("sr1", "5\n")
	check(filepath.Join(dev, "sr1"))
	add("sr2", "5\n")
	if _, err := findOpticalDrive(sys, dev); err == nil {
		t.Fatal("ambiguous drives accepted")
	}
	os.Remove(filepath.Join(dev, "sr2"))
	check(filepath.Join(dev, "sr1"))
}
func TestAutoDriveReplacementClearsOldSession(t *testing.T) {
	changes := []string{}
	d := &AutoDrive{drive: Drive{Device: "/dev/sr0"}, detect: func() (string, error) { return "/dev/sr1", nil }, OnChange: func(p string) { changes = append(changes, p) }}
	got, err := d.Read()
	if err != nil || got.ID != "" || d.DevicePath() != "/dev/sr1" || len(changes) != 1 {
		t.Fatalf("replacement: %+v %v %+v", got, err, d)
	}
	d.detect = func() (string, error) { return "", nil }
	if _, err = d.Read(); err != nil {
		t.Fatal(err)
	}
	if d.DevicePath() != "" {
		t.Fatal("retained disconnected drive")
	}
	if err = d.Eject(); err == nil {
		t.Fatal("eject accepted without drive")
	}
}
