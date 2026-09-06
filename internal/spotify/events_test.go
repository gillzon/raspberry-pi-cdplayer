package spotify

import "testing"

func TestMetadataAndDisconnect(t *testing.T) {
	m := &Manager{}
	m.Apply(Event{Kind: "session_connected"})
	m.Apply(Event{Kind: "track_changed", TrackID: "a", Title: "Song", Artist: "Artist", Album: "Album", Cover: "https://i.scdn.co/image/example", DurationMS: 180000})
	m.Apply(Event{Kind: "playing", TrackID: "a", PositionMS: 12000})
	if !m.State.Connected || m.State.Playback != "playing" || m.State.Now.Title != "Song" || m.State.Now.Cover == "" || m.State.Now.PositionMS != 12000 {
		t.Fatalf("state: %+v", m.State)
	}
	m.Apply(Event{Kind: "paused", TrackID: "a", PositionMS: 13000})
	if !m.State.Connected || m.State.Playback != "paused" || m.State.Now.PositionMS != 13000 {
		t.Fatal("pause lost connection or position")
	}
	m.Apply(Event{Kind: "track_changed", TrackID: "b", Title: "Next"})
	if m.State.Now.Cover != "" || m.State.Now.PositionMS != 0 {
		t.Fatal("previous track metadata leaked")
	}
	m.Apply(Event{Kind: "position_correction", TrackID: "a", PositionMS: 60000})
	if m.State.Now.PositionMS != 0 {
		t.Fatal("old track position applied")
	}
	m.Apply(Event{Kind: "session_disconnected"})
	if m.State.Connected || m.State.Now.Title != "" || m.State.Playback != "disconnected" {
		t.Fatal("disconnect retained metadata")
	}
}
func TestEventEnvironmentAndArtworkValidation(t *testing.T) {
	t.Setenv("PLAYER_EVENT", "track_changed")
	t.Setenv("NAME", "Track")
	t.Setenv("ARTISTS", "One\nTwo")
	t.Setenv("COVERS", "https://i.scdn.co/image/large\nhttps://i.scdn.co/image/small")
	t.Setenv("DURATION_MS", "123000")
	e := eventFromEnv()
	if e.Title != "Track" || e.Artist != "One, Two" || e.Cover != "https://i.scdn.co/image/large" || e.DurationMS != 123000 {
		t.Fatalf("event: %+v", e)
	}
	m := &Manager{}
	e.Cover = "javascript:alert(1)"
	m.Apply(e)
	if m.State.Now.Cover != "" {
		t.Fatal("unsafe artwork URL accepted")
	}
}
