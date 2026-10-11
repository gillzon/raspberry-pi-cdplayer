package driveio

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOperationsAreExclusiveAndCancelledWaiterDoesNotTakeAccess(t *testing.T) {
	a := New()
	hold, _ := a.Acquire(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if release, err := a.Acquire(ctx); err == nil {
		release()
		t.Fatal("overlapping operation")
	}
	hold()
	var active atomic.Int32
	var workers sync.WaitGroup
	for i := 0; i < 10; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				release, err := a.Acquire(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if active.Add(1) != 1 {
					t.Error("drive operations overlapped")
				}
				active.Add(-1)
				release()
			}
		}()
	}
	workers.Wait()
}
