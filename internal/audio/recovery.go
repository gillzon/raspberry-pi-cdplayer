package audio

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/driveio"
)

const maxRecoveryAttempts = 2

var errReadPaused = errors.New("CD cache paused")
var errInvalidRead = errors.New("invalid audio read size")

// sessionReader is owned by the cache worker. The access lease covers helper
// exit too: a timed-out read must actually release the drive before any probe,
// eject or replacement helper can touch it.
type sessionReader struct {
	s        *session
	access   *driveio.Access
	device   string
	layout   []Layout
	reopen   OpenReader
	validate func(string, string) error
	reader   Reader
}

func (r *sessionReader) open(open OpenReader) error {
	release, err := r.access.Acquire(r.s.ctx)
	if err != nil {
		return err
	}
	defer release()
	r.reader, err = open(r.s.ctx, r.device, r.layout)
	return err
}

func (r *sessionReader) close() {
	if r.reader == nil {
		return
	}
	// Cleanup must run even after the session was cancelled.
	release, _ := r.access.Acquire(context.Background())
	defer release()
	r.reader.Close()
	r.reader = nil
}

func (r *sessionReader) readBlock(start, count int) (data []byte, err error) {
	release, err := r.access.Acquire(r.s.ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	// A pause may arrive while waiting behind a status check.
	r.s.mu.Lock()
	paused := r.s.paused
	r.s.mu.Unlock()
	if paused {
		return nil, errReadPaused
	}
	if r.reader == nil {
		r.s.mu.Lock()
		r.s.attempts++
		r.s.mu.Unlock()
		if r.validate != nil {
			if err := r.validate(r.device, r.s.id); err != nil {
				return nil, err
			}
		}
		if err := r.s.ctx.Err(); err != nil {
			return nil, err
		}
		r.s.mu.Lock()
		paused = r.s.paused
		r.s.mu.Unlock()
		if paused {
			r.s.mu.Lock()
			r.s.attempts--
			r.s.mu.Unlock()
			return nil, errReadPaused
		}
		r.reader, err = r.reopen(r.s.ctx, r.device, r.layout)
		if err != nil {
			return nil, err
		}
	}
	defer func() {
		if err != nil && !errors.Is(err, errReadPaused) || r.s.ctx.Err() != nil {
			r.reader.Close()
			r.reader = nil
		}
	}()
	if err = r.s.ctx.Err(); err != nil {
		return nil, err
	}
	r.s.mu.Lock()
	paused = r.s.paused
	r.s.mu.Unlock()
	if paused {
		return nil, errReadPaused
	}
	// Publish each valid short response before issuing another physical read.
	// The cache tracks the prefix and requests only the missing sectors next.
	data, err = r.reader.Read(start, count)
	if len(data)%sectorBytes != 0 || len(data) > count*sectorBytes || (len(data) == 0 && err == nil) {
		return nil, errInvalidRead
	}
	return data, err
}

func (s *session) recoverRead(err error) bool {
	s.mu.Lock()
	s.lastReadError = err.Error()
	if s.attempts < maxRecoveryAttempts {
		s.recovering = true
		s.signal()
		s.mu.Unlock()
		slog.Warn("CD read failed; reopening reader", "error", err)
		return true
	}
	s.readErr, s.recovering = err, false
	s.signal()
	s.mu.Unlock()
	slog.Error("CD read recovery exhausted; retaining cached audio", "error", err)
	select {
	case <-s.ctx.Done():
		return false
	case <-s.retry:
		return true
	}
}

// Retry explicitly grants another bounded recovery attempt sequence. Cached
// blocks and URLs survive. Fatal layout/storage errors require a new session.
func (c *Cache) Retry() bool {
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil || s.readErr == nil || s.ctx.Err() != nil {
		return false
	}
	s.readErr, s.attempts, s.recovering = nil, 0, true
	s.signal()
	select {
	case s.retry <- struct{}{}:
	default:
	}
	return true
}
