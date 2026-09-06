package disc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeEject(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "eject"), []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestEjectUsesConfiguredDeviceAsOneArgument(t *testing.T) {
	fakeEject(t, `[ "$#" = 3 ] && [ "$1" = --verbose ] && [ "$2" = -- ] && [ "$3" = '/dev/test drive' ]`)
	if err := ejectDevice(context.Background(), "/dev/test drive"); err != nil {
		t.Fatal(err)
	}
}

func TestEjectReportsUtilityError(t *testing.T) {
	fakeEject(t, "echo 'Permission denied opening drive' >&2\nexit 1\n")
	err := ejectDevice(context.Background(), "/dev/sr0")
	if err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("error: %v", err)
	}
}

func TestEjectMissingCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := ejectDevice(context.Background(), "/dev/sr0")
	if err == nil || !strings.Contains(err.Error(), "sudo apt install eject") {
		t.Fatalf("error: %v", err)
	}
}

func TestEjectTimeout(t *testing.T) {
	fakeEject(t, "exec /bin/sleep 10\n")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := ejectDevice(ctx, "/dev/sr0"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error: %v", err)
	}
}
