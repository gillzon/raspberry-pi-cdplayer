package player

import (
	"fmt"
	"testing"
)

func TestLatestTrackRequestWinsWithoutWaiting(t *testing.T) {
	q := NewTrackRequests()
	q.Update("disc", []int{1, 2, 3}, true)
	if err := q.Submit(2, "disc"); err != nil {
		t.Fatal(err)
	}
	running := q.Take()
	if err := q.Submit(1, "disc"); err != nil {
		t.Fatal(err)
	}
	if err := q.Submit(3, "disc"); err != nil {
		t.Fatal(err)
	}
	q.Complete(running, fmt.Errorf("old request failed"))
	if s := q.Snapshot(); s.Track != 3 || !s.Pending || s.Error != "" {
		t.Fatal(s)
	}
	next := q.Take()
	if next.Track != 3 {
		t.Fatal("queued intermediate selection retained")
	}
	q.Complete(next, nil)
	if q.Snapshot().Pending {
		t.Fatal("selection not acknowledged")
	}
}
func TestTrackRequestsRejectStaleDiscAndSource(t *testing.T) {
	q := NewTrackRequests()
	q.Update("new", []int{1, 3}, true)
	if q.Submit(1, "old") == nil || q.Submit(2, "new") == nil {
		t.Fatal("invalid selection accepted")
	}
	q.Submit(3, "new")
	q.Update("new", []int{1, 3}, false)
	if q.Take() != nil || q.Snapshot().Pending || q.Submit(1, "new") == nil {
		t.Fatal("Spotify source allowed queued CD playback")
	}
}
func TestManualStopCancelsWaitingTrackSelection(t *testing.T) {
	q := NewTrackRequests()
	q.Update("disc", []int{1, 2}, true)
	q.Submit(2, "disc")
	running := q.Take()
	q.Submit(1, "disc")
	q.Cancel()
	q.Complete(running, nil)
	if q.Take() != nil || q.Snapshot().Pending {
		t.Fatal("manual stop left a queued playback request")
	}
}
