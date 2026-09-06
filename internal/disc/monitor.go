package disc

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Monitor isolates potentially blocking drive status/TOC ioctls from controls.
// All calls to the underlying drive, including eject, use this one worker.
type Monitor struct {
	Updates      chan struct{}
	observer     func(Disc, error)
	mu           sync.RWMutex
	disc         Disc
	err          error
	path         string
	probeStarted time.Time
	eject        chan ejectRequest
	ctx          context.Context
}
type ejectRequest struct {
	ctx  context.Context
	done chan error
}
type monitoredDrive interface {
	Read() (Disc, error)
	Eject() error
	DevicePath() string
}

func NewMonitor(ctx context.Context, d monitoredDrive, interval time.Duration) *Monitor {
	m := &Monitor{Updates: make(chan struct{}, 1), ctx: ctx, err: fmt.Errorf("detecting CD drive"), eject: make(chan ejectRequest)}
	go func() {
		sample := func() time.Duration {
			started := time.Now()
			m.mu.Lock()
			m.probeStarted = started
			m.mu.Unlock()
			v, err := d.Read()
			path := d.DevicePath()
			duration := time.Since(started)
			delay := probeDelay(interval, duration)
			if duration >= time.Second {
				slog.Warn("slow physical CD drive probe", "device", path, "duration", duration, "next_probe_in", delay, "error", err)
			}
			m.mu.Lock()
			m.probeStarted = time.Time{}
			changed := m.disc.ID != v.ID || m.path != path || fmt.Sprint(m.err) != fmt.Sprint(err)
			m.disc, m.err, m.path = v, err, path
			observer := m.observer
			m.mu.Unlock()
			if changed && observer != nil {
				observer(v, err)
			}
			if changed {
				select {
				case m.Updates <- struct{}{}:
				default:
				}
			}
			return delay
		}
		// Start the timer after the physical call completes: a ticker queues
		// ticks during slow I/O and can otherwise cause back-to-back probes.
		timer := time.NewTimer(sample())
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				timer.Reset(sample())
			case r := <-m.eject:
				err := r.ctx.Err()
				if err == nil {
					err = d.Eject()
				}
				if err == nil {
					m.mu.Lock()
					m.disc = Disc{}
					m.err = nil
					m.mu.Unlock()
				}
				r.done <- err
			}
		}
	}()
	return m
}

func probeDelay(interval, duration time.Duration) time.Duration {
	// Give the shared drive a quiet interval after expensive status calls.
	// Keep normal insertion polling fast and bound the added detection delay.
	return max(interval, min(2*duration, 5*time.Second))
}
func (m *Monitor) Read() (Disc, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.probeStarted.IsZero() && time.Since(m.probeStarted) >= 5*time.Second {
		return m.disc, fmt.Errorf("CD drive probe has not returned for at least %s (device %s); waiting for Linux drive I/O", time.Since(m.probeStarted).Truncate(5*time.Second), m.path)
	}
	return m.disc, m.err
}
func (m *Monitor) DevicePath() string { m.mu.RLock(); defer m.mu.RUnlock(); return m.path }
func (m *Monitor) Eject() error {
	ctx, cancel := context.WithTimeout(m.ctx, 8*time.Second)
	defer cancel()
	r := ejectRequest{ctx: ctx, done: make(chan error, 1)}
	select {
	case m.eject <- r:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-r.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetObserver installs a nonblocking media-change notification before playback.
func (m *Monitor) SetObserver(f func(Disc, error)) { m.mu.Lock(); m.observer = f; m.mu.Unlock() }
