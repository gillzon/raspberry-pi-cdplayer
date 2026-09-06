package disc

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type slowMonitoredDrive struct {
	started chan struct{}
	unblock chan struct{}
	once    sync.Once
}

func (d *slowMonitoredDrive) Read() (Disc, error) {
	d.once.Do(func() { close(d.started) })
	<-d.unblock
	return Disc{ID: "disc", Tracks: []int{1}}, nil
}
func (d *slowMonitoredDrive) Eject() error       { return nil }
func (d *slowMonitoredDrive) DevicePath() string { return "/dev/fake" }
func TestSlowProbeDoesNotBlockStatusReaders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drive := &slowMonitoredDrive{started: make(chan struct{}), unblock: make(chan struct{})}
	defer close(drive.unblock)
	m := NewMonitor(ctx, drive, time.Second)
	<-drive.started
	done := make(chan error, 1)
	go func() { _, err := m.Read(); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("missing startup status")
		}
	case <-time.After(time.Second):
		t.Fatal("status blocked on physical probe")
	}
}

func TestBlockedProbeIsNotReportedAsOldDriveStatus(t *testing.T) {
	m := &Monitor{path: "/dev/fake", probeStarted: time.Now().Add(-6 * time.Second)}
	_, err := m.Read()
	if err == nil || !strings.Contains(err.Error(), "probe has not returned") {
		t.Fatalf("blocked probe hidden: %v", err)
	}
	m.probeStarted = time.Time{}
	if _, err := m.Read(); err != nil {
		t.Fatalf("completed probe still blocked: %v", err)
	}
}
