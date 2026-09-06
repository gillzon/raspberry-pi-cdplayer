package player

import (
	"fmt"
	"slices"
	"sync"
)

type TrackSelection struct {
	Track   int    `json:"track"`
	Pending bool   `json:"pending"`
	Error   string `json:"error"`
}
type TrackRequest struct {
	Track      int
	DiscID     string
	generation uint64
}

// TrackRequests is a bounded latest-request mailbox. HTTP does not wait for
// drive reads or MPD acknowledgements; a newer selection replaces queued work.
type TrackRequests struct {
	OnSelect   func(int) // configure before use; invoked in submission order
	mu         sync.Mutex
	Wake       chan struct{}
	discID     string
	tracks     []int
	allowed    bool
	generation uint64
	next       *TrackRequest
	state      TrackSelection
}

func NewTrackRequests() *TrackRequests { return &TrackRequests{Wake: make(chan struct{}, 1)} }
func (q *TrackRequests) Update(discID string, tracks []int, allowed bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.discID != discID || !slices.Equal(q.tracks, tracks) || !allowed {
		q.generation++
		q.next = nil
		q.state = TrackSelection{}
	}
	q.discID = discID
	q.tracks = slices.Clone(tracks)
	q.allowed = allowed
}
func (q *TrackRequests) Submit(track int, discID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.allowed {
		return fmt.Errorf("select Play CD before choosing a track")
	}
	if discID != "" && discID != q.discID {
		return fmt.Errorf("disc changed; select a track on the current disc")
	}
	if !slices.Contains(q.tracks, track) {
		return fmt.Errorf("track is not on the current disc")
	}
	q.generation++
	q.next = &TrackRequest{Track: track, DiscID: q.discID, generation: q.generation}
	q.state = TrackSelection{Track: track, Pending: true}
	if q.OnSelect != nil {
		q.OnSelect(track)
	}
	select {
	case q.Wake <- struct{}{}:
	default:
	}
	return nil
}
func (q *TrackRequests) Take() *TrackRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	r := q.next
	q.next = nil
	return r
}
func (q *TrackRequests) Complete(r *TrackRequest, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if r.generation != q.generation {
		return
	}
	q.state.Pending = false
	if err != nil {
		q.state.Error = err.Error()
	}
}
func (q *TrackRequests) Snapshot() TrackSelection { q.mu.Lock(); defer q.mu.Unlock(); return q.state }

func (q *TrackRequests) Cancel() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.generation++
	q.next = nil
	q.state = TrackSelection{}
}
