// Package driveio coordinates physical drive operations across the monitor and
// audio reader. Waiting for access never starts an optical-read timeout.
package driveio

import "context"

type Access struct{ token chan struct{} }

func New() *Access {
	a := &Access{token: make(chan struct{}, 1)}
	a.token <- struct{}{}
	return a
}

// Acquire returns exclusive access until release is called. A blocked physical
// operation must retain access until it actually returns, even after cancellation.
// A nil Access is useful for standalone readers and the direct-MPD fallback.
func (a *Access) Acquire(ctx context.Context) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a == nil {
		return func() {}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.token:
		if err := ctx.Err(); err != nil {
			a.token <- struct{}{}
			return nil, err
		}
		return func() { a.token <- struct{}{} }, nil
	}
}
