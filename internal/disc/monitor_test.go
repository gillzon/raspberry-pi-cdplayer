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

func TestProbeDelay(t *testing.T) {
	for _, tc := range []struct{ interval, duration, want time.Duration }{
		{time.Second, 10 * time.Millisecond, time.Second},
		{time.Second, 3 * time.Second, 5 * time.Second},
		{time.Second, 10 * time.Second, 5 * time.Second},
		{10 * time.Second, time.Second, 10 * time.Second},
	} {
		if got := probeDelay(tc.interval, tc.duration); got != tc.want {
			t.Fatalf("delay=%s want %s", got, tc.want)
		}
	}
}

type controlledProbe struct {
	starts  chan struct{}
	release chan struct{}
	ctx     context.Context
}

func (d *controlledProbe) Read() (Disc, error) {
	select {
	case d.starts <- struct{}{}:
	case <-d.ctx.Done():
		return Disc{}, d.ctx.Err()
	}
	select {
	case <-d.release:
	case <-d.ctx.Done():
		return Disc{}, d.ctx.Err()
	}
	return Disc{ID: "disc", Tracks: []int{1}}, nil
}
func (d *controlledProbe) Eject() error       { return nil }
func (d *controlledProbe) DevicePath() string { return "/dev/fake" }
func TestSlowProbeDoesNotImmediatelyRepeat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &controlledProbe{starts: make(chan struct{}, 2), release: make(chan struct{}, 2), ctx: ctx}
	m := NewMonitor(ctx, d, 20*time.Millisecond)
	select {
	case <-d.starts:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	time.Sleep(80 * time.Millisecond) // several polling ticks pass during the read
	d.release <- struct{}{}
	select {
	case <-m.Updates:
	case <-time.After(time.Second):
		t.Fatal("probe did not finish")
	}
	select {
	case <-d.starts:
		t.Fatal("slow probe immediately repeated")
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-d.starts:
	case <-time.After(time.Second):
		t.Fatal("polling did not resume")
	}
}
