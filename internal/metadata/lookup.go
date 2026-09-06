package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
)

type Track struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
}

type Info struct {
	DiscID    string        `json:"disc_id"`
	Status    string        `json:"status"`
	Album     string        `json:"album"`
	Artist    string        `json:"artist"`
	ReleaseID string        `json:"release_id"`
	Tracks    map[int]Track `json:"tracks"`
	CoverURL  string        `json:"cover_url"`
	Message   string        `json:"message"`
	Matches   int           `json:"matches"`
}

type credit struct {
	Name   string `json:"name"`
	Join   string `json:"joinphrase"`
	Artist struct {
		Name string `json:"name"`
	} `json:"artist"`
}

func artist(credits []credit) string {
	var b strings.Builder
	for _, c := range credits {
		name := c.Name
		if name == "" {
			name = c.Artist.Name
		}
		b.WriteString(name + c.Join)
	}
	return b.String()
}

type release struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Credits []credit `json:"artist-credit"`
	Media   []struct {
		Discs []struct {
			ID string `json:"id"`
		} `json:"discs"`
		Tracks []struct {
			Position  int      `json:"position"`
			Title     string   `json:"title"`
			Credits   []credit `json:"artist-credit"`
			Recording struct {
				Title   string   `json:"title"`
				Credits []credit `json:"artist-credit"`
			} `json:"recording"`
		} `json:"tracks"`
	} `json:"media"`
}

func selectRelease(body []byte, d disc.Disc) (Info, error) {
	var result struct {
		Releases []release `json:"releases"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return Info{}, err
	}
	info := Info{DiscID: d.ID, Status: "not_found", Message: "No matching album found"}
	for _, r := range result.Releases {
		for _, medium := range r.Media {
			match := false
			for _, cd := range medium.Discs {
				if cd.ID == d.MusicBrainzID {
					match = true
				}
			}
			if !match || len(medium.Tracks) != len(d.Tracks) {
				continue
			}
			tracks := make(map[int]Track)
			for _, t := range medium.Tracks {
				if t.Position < 1 || t.Position > len(d.Tracks) {
					continue
				}
				title := t.Title
				if title == "" {
					title = t.Recording.Title
				}
				name := artist(t.Credits)
				if name == "" {
					name = artist(t.Recording.Credits)
				}
				if name == "" {
					name = artist(r.Credits)
				}
				tracks[d.Tracks[t.Position-1]] = Track{Title: title, Artist: name}
			}
			if len(tracks) != len(d.Tracks) {
				continue
			}
			info.Matches++
			if info.Status != "ready" {
				info.Status = "ready"
				info.Album = r.Title
				info.Artist = artist(r.Credits)
				info.ReleaseID = r.ID
				info.Tracks = tracks
				info.Message = ""
			}
			break
		}
	}
	if info.Matches > 1 {
		info.Message = "Multiple editions match this disc; showing the first matching edition"
	}
	return info, nil
}

func (m *Manager) get(ctx context.Context, address string, limit int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "raspberry-pi-cdplayer/0.1 (https://github.com/gillzon/raspberry-pi-cdplayer)")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if int64(len(body)) > limit {
		return nil, resp.StatusCode, fmt.Errorf("lookup response too large")
	}
	return body, resp.StatusCode, nil
}

func (m *Manager) lookup(ctx context.Context, d disc.Disc) (Info, error) {
	// Only the worker calls this method, keeping starts over a second apart.
	if delay := time.Until(m.nextRequest); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return Info{}, ctx.Err()
		case <-timer.C:
		}
	}
	m.nextRequest = time.Now().Add(1100 * time.Millisecond)
	query := url.Values{"inc": {"recordings artist-credits"}, "fmt": {"json"}, "cdstubs": {"no"}}
	body, status, err := m.get(ctx, m.musicBrainz+"/ws/2/discid/"+url.PathEscape(d.MusicBrainzID)+"?"+query.Encode(), 4<<20)
	if err != nil {
		return Info{}, err
	}
	if status == 404 {
		return Info{DiscID: d.ID, Status: "not_found", Message: "No matching album found"}, nil
	}
	if status != 200 {
		return Info{}, fmt.Errorf("MusicBrainz returned HTTP %s", strconv.Itoa(status))
	}
	return selectRelease(body, d)
}
