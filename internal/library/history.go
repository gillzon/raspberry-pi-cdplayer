package library

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"math"
	"strconv"
	"time"
)

// PlayCounter observes MPD, not browser requests. Pauses and seek jumps do not
// add listening time; a new queue entry or a restart begins a new play.
type PlayCounter struct {
	key               string
	elapsed, listened float64
	last              time.Time
	playing, counted  bool
}

func (p *PlayCounter) Observe(source string, track *Track, status map[string]string, now time.Time) string {
	if source != "usb" || status["state"] == "stop" {
		*p = PlayCounter{}
		return ""
	}
	if track == nil || status["error"] != "" {
		p.playing, p.last = false, now
		return ""
	}
	elapsed, err := strconv.ParseFloat(status["elapsed"], 64)
	if err != nil || math.IsNaN(elapsed) || math.IsInf(elapsed, 0) {
		return ""
	}
	key := track.ID + ":" + status["songid"] + ":" + status["song"]
	playing := status["state"] == "play"
	if key != p.key || elapsed+1 < p.elapsed {
		*p = PlayCounter{key: key, elapsed: elapsed, last: now, playing: playing}
		return ""
	}
	wall, advance := now.Sub(p.last).Seconds(), elapsed-p.elapsed
	if p.playing && playing && wall > 0 && wall <= 10 && advance > 0 && advance <= wall+2 {
		p.listened += math.Min(wall, advance)
	}
	p.elapsed, p.last, p.playing = elapsed, now, playing
	threshold := 30.0
	if track.Duration > 0 {
		threshold = math.Min(threshold, track.Duration/2)
	}
	if !p.counted && p.listened >= threshold {
		p.counted = true
		return track.ID
	}
	return ""
}

type playEvent struct {
	ID       string `json:"id"`
	TrackID  string `json:"track_id"`
	PlayedAt string `json:"played_at"`
}

// QueuePlay keeps SQLite writes off the playback controller's goroutine.
func (l *Library) QueuePlay(id string) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		slog.Warn("create listening event", "error", err)
		return
	}
	event := playEvent{hex.EncodeToString(token[:]), id, time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")}
	select {
	case l.plays <- event:
	default:
		slog.Warn("listening history queue full", "track", id)
	}
}

func (l *Library) RunHistory(ctx context.Context) {
	save := func(event playEvent) {
		data, _ := json.Marshal(event)
		// The event ID makes retries safe even if a previous write committed.
		for attempt := 0; attempt < 3; attempt++ {
			writeCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			_, err := l.run(writeCtx, "record-play", string(data))
			cancel()
			if err == nil {
				return
			}
			if attempt == 2 {
				slog.Warn("save listening history", "error", err)
			}
		}
	}
	for {
		select {
		case event := <-l.plays:
			save(event)
		case <-ctx.Done():
			for {
				select {
				case event := <-l.plays:
					save(event)
				default:
					return
				}
			}
		}
	}
}

type PlaylistChange struct {
	Action string `json:"action"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	SongID string `json:"song_id"`
}

func (l *Library) ChangePlaylist(ctx context.Context, change PlaylistChange) (Collection, error) {
	data, err := json.Marshal(change)
	if err != nil {
		return Collection{}, err
	}
	response, err := l.run(ctx, "playlist-change", string(data))
	var result Collection
	if err == nil {
		err = json.Unmarshal(response, &result)
	}
	return result, err
}

func (l *Library) Playlist(ctx context.Context, id string) ([]Track, error) {
	data, err := l.run(ctx, "playlist-queue", id)
	var tracks []Track
	if err == nil {
		err = json.Unmarshal(data, &tracks)
	}
	if err != nil {
		return nil, err
	}
	return l.available(ctx, tracks)
}
