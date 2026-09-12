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

// The physical worker owns both invalidation and probing.
type refreshableProbe struct {
	controlledProbe
	invalidated chan struct{}
}

func (d *refreshableProbe) InvalidateTOC() { d.invalidated <- struct{}{} }

func TestRefreshDuringProbeDiscardsOldSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &refreshableProbe{controlledProbe: controlledProbe{starts: make(chan struct{}, 2), release: make(chan struct{}, 2), ctx: ctx}, invalidated: make(chan struct{}, 1)}
	m := NewMonitor(ctx, d, time.Hour)
	<-d.starts
	m.Refresh()
	d.release <- struct{}{}
	select {
	case <-d.invalidated:
	case <-time.After(time.Second):
		t.Fatal("refresh waited for the polling timer")
	}
	<-d.starts
	if _, err := m.Read(); err == nil {
		t.Fatal("old in-flight probe overwrote refresh status")
	}
	d.release <- struct{}{}
	select {
	case <-m.Updates:
	case <-time.After(time.Second):
		t.Fatal("fresh probe did not wake player")
	}
	if got, err := m.Read(); err != nil || got.ID != "disc" {
		t.Fatalf("refresh failed: %+v %v", got, err)
	}
}

type ejectableProbe struct {
	mu   sync.Mutex
	disc Disc
}

func (d *ejectableProbe) Read() (Disc, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.disc, nil
}
func (d *ejectableProbe) Eject() error       { return nil }
func (d *ejectableProbe) DevicePath() string { return "/dev/fake" }

func TestEjectNotifiesRemovalAndSuppressesStaleTOC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	album := Disc{ID: "album", Tracks: []int{1}}
	d := &ejectableProbe{disc: album}
	m := NewMonitor(ctx, d, time.Hour)
	waitUpdate := func() {
		t.Helper()
		select {
		case <-m.Updates:
		case <-time.After(time.Second):
			t.Fatal("missing monitor update")
		}
	}
	waitUpdate()
	removed := make(chan struct{}, 1)
	m.SetObserver(func(d Disc, err error) {
		if err == nil && d.ID == "" {
			select {
			case removed <- struct{}{}:
			default:
			}
		}
	})
	if err := m.Eject(); err != nil {
		t.Fatal(err)
	}
	waitUpdate()
	select {
	case <-removed:
	default:
		t.Fatal("eject did not invalidate cached audio")
	}
	for _, tc := range []struct {
		physical Disc
		want     string
	}{
		{album, ""}, // Firmware still reports the old TOC as the tray opens.
		{album, ""},
		{Disc{}, ""},
		{album, "album"}, // The same album can play after a real reinsertion.
	} {
		d.mu.Lock()
		d.disc = tc.physical
		d.mu.Unlock()
		m.Refresh()
		waitUpdate()
		if got, err := m.Read(); err != nil || got.ID != tc.want {
			t.Fatalf("disc after eject: %+v %v, want %q", got, err, tc.want)
		}
	}
}
