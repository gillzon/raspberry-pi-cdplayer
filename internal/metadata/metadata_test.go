package metadata

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
)

const fixture = `{"releases":[{"id":"release-1","title":"Album","artist-credit":[{"name":"Artist A","joinphrase":" & "},{"artist":{"name":"Artist B"}}],"media":[{"discs":[{"id":"other-disc"}],"tracks":[{"position":1,"title":"Wrong disc"}]},{"discs":[{"id":"mb-id"}],"tracks":[{"position":1,"title":"Song one","artist-credit":[{"name":"Guest"}]},{"position":2,"recording":{"title":"Song two"}}]}]}]}`

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func await(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for metadata")
}

func TestReleaseAndTrackMapping(t *testing.T) {
	d := disc.Disc{ID: "local-id", MusicBrainzID: "mb-id", Tracks: []int{1, 2}}
	i, err := selectRelease([]byte(fixture), d)
	if err != nil || i.Album != "Album" || i.Artist != "Artist A & Artist B" || i.Tracks[1].Artist != "Guest" || i.Tracks[2].Title != "Song two" || i.Tracks[2].Artist != i.Artist {
		t.Fatalf("metadata: %+v %v", i, err)
	}
	d.MusicBrainzID = "unmatched"
	i, err = selectRelease([]byte(fixture), d)
	if err != nil || i.Status != "not_found" {
		t.Fatalf("wrong disc matched: %+v %v", i, err)
	}
}

func TestBackgroundArtAndOfflineCache(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := disc.Disc{ID: "local-id", MusicBrainzID: "mb-id", Tracks: []int{1, 2}}
	m := New(t.TempDir())
	artStarted := make(chan struct{})
	allowArt := make(chan struct{})
	m.client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		if strings.Contains(r.URL.Path, "/discid/") {
			return response(200, fixture), nil
		}
		close(artStarted)
		select {
		case <-allowArt:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return response(200, "\xff\xd8\xff\xe0test-image"), nil
	})}
	go m.Run(ctx)
	m.Observe(ctx, d)
	select {
	case <-artStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("art lookup not started")
	}
	if i := m.Snapshot(d.ID); i.Album != "Album" || i.CoverURL != "" {
		t.Fatalf("titles blocked by artwork: %+v", i)
	}
	close(allowArt)
	await(t, func() bool { return m.Snapshot(d.ID).CoverURL != "" })
	if len(m.Art(d.ID)) == 0 {
		t.Fatal("missing local artwork")
	}
	// A new process can use both metadata and art without network access.
	offline := New(m.cacheDir)
	offline.client = &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		t.Error("network used for cached album")
		return nil, errors.New("offline")
	})}
	go offline.Run(ctx)
	offline.Observe(ctx, d)
	await(t, func() bool { return offline.Snapshot(d.ID).Status == "ready" })
	if offline.Snapshot(d.ID).Album != "Album" || len(offline.Art(d.ID)) == 0 {
		t.Fatal("offline cache incomplete")
	}
}

func TestDiscRemovalCancelsAndDiscardsLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := New("")
	started := make(chan struct{})
	canceled := make(chan struct{})
	m.client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(canceled)
		return nil, r.Context().Err()
	})}
	go m.Run(ctx)
	m.Observe(ctx, disc.Disc{ID: "old", MusicBrainzID: "mb-id", Tracks: []int{1, 2}})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("lookup not started")
	}
	m.Observe(ctx, disc.Disc{})
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("lookup not canceled")
	}
	if got := m.Snapshot(""); got.Status != "empty" || got.Album != "" {
		t.Fatalf("stale metadata: %+v", got)
	}
}

func TestLookupFailureAndMissingCover(t *testing.T) {
	for _, status := range []int{404, 503, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := New("")
			m.client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/discid/") {
					return response(status, fixture), nil
				}
				return response(404, ""), nil
			})}
			go m.Run(ctx)
			m.Observe(ctx, disc.Disc{ID: "disc", MusicBrainzID: "mb-id", Tracks: []int{1, 2}})
			want := map[int]string{404: "not_found", 503: "error", 200: "ready"}[status]
			await(t, func() bool { return m.Snapshot("disc").Status == want })
		})
	}
}
