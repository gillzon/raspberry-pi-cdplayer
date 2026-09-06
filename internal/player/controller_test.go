package player

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
)

type fakeDrive struct {
	disc disc.Disc
	err  error
}

func (d *fakeDrive) Read() (disc.Disc, error) { return d.disc, d.err }

type fakeBackend struct {
	fresh                           bool
	connectErr, startErr, healthErr error
	starts                          [][]int
	clears                          int
}

func (b *fakeBackend) Connect(context.Context) (bool, error) {
	fresh := b.fresh
	b.fresh = false
	return fresh, b.connectErr
}
func (b *fakeBackend) Start(tracks []int) error {
	b.starts = append(b.starts, append([]int(nil), tracks...))
	return b.startErr
}
func (b *fakeBackend) Clear() error         { b.clears++; return b.connectErr }
func (b *fakeBackend) PlaybackError() error { return b.healthErr }

func TestDiscLifecycle(t *testing.T) {
	audio := disc.Disc{ID: "album", Tracks: []int{1, 2, 3}}
	drive, backend := &fakeDrive{}, &fakeBackend{fresh: true}
	c := &Controller{Drive: drive, Backend: backend}
	step := func(d disc.Disc) {
		t.Helper()
		drive.disc = d
		if err := c.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	step(disc.Disc{}) // clean stale queue at boot
	step(audio)
	step(audio) // includes pause, manual stop, and natural album completion
	if len(backend.starts) != 1 {
		t.Fatal("unchanged disc restarted")
	}
	step(disc.Disc{}) // removal
	step(audio)       // same disc reinserted
	step(disc.Disc{ID: "data"})
	step(disc.Disc{ID: "mixed", Tracks: []int{2, 3}})
	if backend.clears != 3 {
		t.Fatalf("clear count: %d", backend.clears)
	}
	if want := [][]int{{1, 2, 3}, {1, 2, 3}, {2, 3}}; !reflect.DeepEqual(backend.starts, want) {
		t.Fatalf("starts = %v; want %v", backend.starts, want)
	}
}

func TestBootWithDiscAndReconnect(t *testing.T) {
	drive := &fakeDrive{disc: disc.Disc{ID: "album", Tracks: []int{1}}}
	backend := &fakeBackend{fresh: true}
	c := &Controller{Drive: drive, Backend: backend}
	for i := 0; i < 2; i++ {
		backend.fresh = true // first connection, then MPD restart
		if err := c.Step(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(backend.starts) != 2 {
		t.Fatalf("starts: %v", backend.starts)
	}
}

func TestTransientFailures(t *testing.T) {
	failure := errors.New("not ready")
	drive := &fakeDrive{disc: disc.Disc{ID: "album", Tracks: []int{1}}}
	backend := &fakeBackend{connectErr: failure}
	c := &Controller{Drive: drive, Backend: backend}
	if c.Step(context.Background()) == nil {
		t.Fatal("missing connection error")
	}
	backend.connectErr, backend.startErr = nil, failure
	if c.Step(context.Background()) == nil {
		t.Fatal("missing start error")
	}
	backend.startErr = nil
	if err := c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	drive.err = failure
	if c.Step(context.Background()) == nil {
		t.Fatal("missing drive error")
	}
	drive.err = nil
	if err := c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(backend.starts) != 2 {
		t.Fatalf("transient read restarted audio: %v", backend.starts)
	}
	backend.healthErr = errors.New("scratched disc")
	for i := 0; i < 2; i++ {
		if c.Step(context.Background()) == nil {
			t.Fatal("missing playback error")
		}
	}
	if len(backend.starts) != 2 {
		t.Fatal("playback error caused restart loop")
	}
}

type ejectDrive struct {
	fakeDrive
	eject func() error
}

func (d *ejectDrive) Eject() error { return d.eject() }

func TestEjectStopsBeforeOpeningAndDoesNotReplayOldDisc(t *testing.T) {
	b := &fakeBackend{}
	d := &ejectDrive{fakeDrive: fakeDrive{disc: disc.Disc{ID: "album", Tracks: []int{1}}}}
	d.eject = func() error {
		if b.clears != 1 {
			t.Fatal("tray opened before clearing playback")
		}
		return nil
	}
	c := &Controller{Drive: d, Backend: b}
	if err := c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Eject(); err != nil {
		t.Fatal(err)
	}
	if c.Disc().ID != "" {
		t.Fatal("disc remains visible after eject")
	}
	if err := c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.starts) != 1 {
		t.Fatal("restarted while tray opens")
	}
	d.disc = disc.Disc{}
	if err := c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.disc = disc.Disc{ID: "album", Tracks: []int{1}}
	if err := c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.starts) != 2 {
		t.Fatal("same disc failed to restart after reinsertion")
	}
}

func TestEjectFailures(t *testing.T) {
	b := &fakeBackend{connectErr: errors.New("MPD unavailable")}
	called := false
	d := &ejectDrive{eject: func() error { called = true; return errors.New("tray locked") }}
	c := &Controller{Drive: d, Backend: b}
	if c.Eject() == nil || called {
		t.Fatal("ejected despite failure to stop playback")
	}
	b.connectErr = nil
	if c.Eject() == nil || !called {
		t.Fatal("eject failure not reported")
	}
}

func TestSpotifyHandoffAndReturn(t *testing.T) {
	ctx := context.Background()
	d := &fakeDrive{disc: disc.Disc{ID: "a", Tracks: []int{1, 2}}}
	b := &fakeBackend{}
	c := &Controller{Drive: d, Backend: b}
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.UseSpotify(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Source() != "spotify" || b.clears != 1 {
		t.Fatal("handoff did not stop CD")
	}
	d.disc = disc.Disc{ID: "b", Tracks: []int{1, 2, 3}}
	d.err = errors.New("USB disconnected")
	for i := 0; i < 3; i++ {
		b.fresh = true
		if err := c.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.starts) != 1 {
		t.Fatal("CD restarted while Spotify selected")
	}
	d.err = nil
	c.UseCD()
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Source() != "cd" || len(b.starts) != 2 || len(b.starts[1]) != 3 {
		t.Fatal("did not start current disc")
	}
}
func TestSpotifyFailedHandoffSuppressesAutoplay(t *testing.T) {
	b := &fakeBackend{connectErr: errors.New("MPD unavailable")}
	c := &Controller{Drive: &fakeDrive{disc: disc.Disc{ID: "a", Tracks: []int{1}}}, Backend: b}
	if c.UseSpotify(context.Background()) == nil {
		t.Fatal("acknowledged failed handoff")
	}
	b.connectErr = nil
	if err := c.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.starts) != 0 || b.clears != 1 {
		t.Fatal("failed handoff allowed autoplay")
	}
}

func TestSpotifySessionAndSinkHandoffsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	b := &fakeBackend{}
	c := &Controller{Drive: &fakeDrive{disc: disc.Disc{ID: "a", Tracks: []int{1}}}, Backend: b}
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := c.UseSpotify(ctx); err != nil {
			t.Fatal(err)
		}
		if err := c.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if b.clears != 1 || len(b.starts) != 1 {
		t.Fatal("repeated handoff changed playback")
	}
	c.UseCD()
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.UseSpotify(ctx); err != nil {
		t.Fatal(err)
	}
	if b.clears != 2 {
		t.Fatal("new Spotify session failed to stop resumed CD")
	}
}

type matchingBackend struct {
	fakeBackend
	matches  bool
	matchErr error
	checks   int
}

func (b *matchingBackend) QueueMatches([]int) (bool, error) { b.checks++; return b.matches, b.matchErr }
func TestReconnectPreservesQueueAfterTrackTimeout(t *testing.T) {
	b := &matchingBackend{matches: true}
	c := &Controller{Drive: &fakeDrive{disc: disc.Disc{ID: "album", Tracks: []int{1, 2}}}, Backend: b}
	ctx := context.Background()
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	b.fresh = true
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.starts) != 1 || b.clears != 0 || b.checks != 1 {
		t.Fatal("reconnect restarted an intact queue")
	}
	b.fresh = true
	b.matches = false
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.starts) != 2 {
		t.Fatal("missing queue was not restored")
	}
}
func TestReconnectQueueCheckFailureRetriesWithoutRestart(t *testing.T) {
	b := &matchingBackend{matches: true}
	c := &Controller{Drive: &fakeDrive{disc: disc.Disc{ID: "album", Tracks: []int{1}}}, Backend: b}
	ctx := context.Background()
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	b.fresh = true
	b.matchErr = errors.New("MPD still busy")
	if err := c.Step(ctx); err == nil {
		t.Fatal("missing check error")
	}
	b.matchErr = nil
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.starts) != 1 || b.checks != 2 {
		t.Fatal("failed check caused restart or was not retried")
	}
}

func TestSpotifyDisconnectWaitsForManualCDStart(t *testing.T) {
	ctx := context.Background()
	d := &fakeDrive{disc: disc.Disc{ID: "a", Tracks: []int{1}}}
	b := &fakeBackend{}
	c := &Controller{Drive: d, Backend: b}
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.UseSpotify(ctx); err != nil {
		t.Fatal(err)
	}
	c.SpotifyDisconnected()
	if c.Source() != "idle" {
		t.Fatal("disconnect did not expose idle state")
	}
	d.disc = disc.Disc{ID: "b", Tracks: []int{1, 2}}
	for i := 0; i < 3; i++ {
		b.fresh = true
		if err := c.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.starts) != 1 {
		t.Fatal("disconnect or disc insertion started CD")
	}
	c.UseCD()
	if err := c.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Source() != "cd" || len(b.starts) != 2 {
		t.Fatal("manual CD start failed")
	}
	if err := c.UseSpotify(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Source() != "spotify" {
		t.Fatal("new Spotify session stayed idle")
	}
}
