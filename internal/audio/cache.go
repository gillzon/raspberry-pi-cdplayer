// Package audio reads each inserted disc through one persistent background
// reader and exposes progressively cached WAV tracks to MPD over loopback HTTP.
package audio

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sectorBytes = 2352
const blockSectors = 75

var ErrPreparing = errors.New("preparing CD audio reader")

type track struct {
	layout Layout
	file   *os.File
	ready  []bool
}
type Status struct {
	DiscID      string `json:"disc_id"`
	CachedBytes int64  `json:"cached_bytes"`
	TotalBytes  int64  `json:"total_bytes"`
	Error       string `json:"error"`
}
type Cache struct {
	lock          *os.File
	temporaryRoot bool
	workers       sync.WaitGroup
	mu            sync.Mutex
	session       *session
	Root          string
	BaseURL       string
	Open          OpenReader
}
type session struct {
	mu             sync.Mutex
	ctx            context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	id, token, dir string
	tracks         []*track
	selected       int
	demand         map[*track]int
	changed        chan struct{}
	err            error
	cached, total  int64
}

func (c *Cache) Observe(id, device string, layout []Layout) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil && c.session.id == id {
		return nil
	}
	if c.session != nil {
		c.session.cancel()
		c.session = nil
	}
	if id == "" || len(layout) == 0 {
		return nil
	}
	totalSectors := 0
	lastNumber, lastEnd := 0, 0
	for _, l := range layout {
		if l.Number <= lastNumber || l.Start < lastEnd {
			return fmt.Errorf("CD audio layout is not ordered")
		}
		totalSectors += l.End - l.Start
		if totalSectors > 450000 {
			return fmt.Errorf("CD audio exceeds cache size limit")
		}
		lastNumber, lastEnd = l.Number, l.End
		if l.Number < 1 || l.Number > 99 || l.Start < 0 || l.End <= l.Start || l.End > 450000 {
			return fmt.Errorf("invalid CD audio layout")
		}
	}
	if c.Root != "" {
		if err := os.MkdirAll(c.Root, 0700); err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp(c.Root, "cd-audio-")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		cancel()
		os.RemoveAll(dir)
		return err
	}
	s := &session{ctx: ctx, cancel: cancel, done: make(chan struct{}), id: id, token: hex.EncodeToString(token), dir: dir, demand: make(map[*track]int), changed: make(chan struct{})}
	cleanup := func() {
		cancel()
		for _, t := range s.tracks {
			t.file.Close()
		}
		os.RemoveAll(dir)
	}
	for _, l := range layout {
		f, e := os.Create(filepath.Join(dir, strconv.Itoa(l.Number)+".wav"))
		if e != nil {
			cleanup()
			return e
		}
		t := &track{layout: l, file: f, ready: make([]bool, (l.End-l.Start+blockSectors-1)/blockSectors)}
		s.tracks = append(s.tracks, t)
		size := int64(l.End-l.Start) * sectorBytes
		s.total += size
		if _, e = f.Write(wavHeader(uint32(size))); e != nil {
			cleanup()
			return e
		}
		if e = f.Truncate(size + 44); e != nil {
			cleanup()
			return e
		}
	}
	c.session = s
	opener := c.Open
	if opener == nil {
		opener = openReader
	}
	c.workers.Add(1)
	go func() { defer c.workers.Done(); defer close(s.done); defer cleanup(); s.run(device, layout, opener) }()
	return nil
}
func (c *Cache) Close() {
	c.mu.Lock()
	if c.session != nil {
		c.session.cancel()
		c.session = nil
	}
	c.mu.Unlock()
}

// Ready prevents publishing a playlist until the reader has validated the TOC
// and produced audio. A failed session remains failed until explicitly retried.
func (c *Cache) Ready() error {
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.cached == 0 {
		return ErrPreparing
	}
	return nil
}
func (c *Cache) TrackURL(number int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return ""
	}
	return c.BaseURL + "/audio/" + c.session.token + "/" + strconv.Itoa(number) + ".wav"
}
func (c *Cache) Select(number int) {
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.tracks {
		if t.layout.Number == number {
			s.selected = i
			s.signal()
			return
		}
	}
}
func (c *Cache) Status() Status {
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	if s == nil {
		return Status{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := Status{DiscID: s.id, CachedBytes: s.cached, TotalBytes: s.total}
	if s.err != nil {
		v.Error = s.err.Error()
	}
	return v
}
func (s *session) signal() { close(s.changed); s.changed = make(chan struct{}) }
func (s *session) fail(err error) {
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return // Disc removal, source switching and shutdown cancel intentionally.
	}
	s.err = err
	s.signal()
	s.mu.Unlock()
	slog.Error("CD audio cache failed", "error", err)
}
func (s *session) run(device string, layout []Layout, open OpenReader) {
	r, err := open(s.ctx, device, layout)
	if err != nil {
		s.fail(err)
		<-s.ctx.Done()
		return
	}
	defer func() {
		if r != nil {
			r.Close()
		}
	}()
	for {
		if s.ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		var target *track
		block := -1
		// Reads requested by the active HTTP stream have priority over read-ahead.
		selected := s.tracks[s.selected]
		// Prime the newly selected track before servicing an old stream's backlog.
		for n := 0; n < min(2, len(selected.ready)); n++ {
			if !selected.ready[n] {
				target = selected
				block = n
				break
			}
		}
		if n, ok := s.demand[selected]; target == nil && ok && !selected.ready[n] {
			target = selected
			block = n
		}
		// Keep reading the selected track sequentially. Seeking away for
		// speculative intros (or old HTTP demand) can drain the live buffer.
		if target == nil {
			for n, ready := range selected.ready {
				if !ready {
					target, block = selected, n
					break
				}
			}
		}
		if target == nil {
			for t, n := range s.demand {
				if !t.ready[n] {
					target, block = t, n
					break
				}
			}
		}
		if target == nil {
			for j := 0; j < len(s.tracks); j++ {
				t := s.tracks[(s.selected+j)%len(s.tracks)]
				for n, ready := range t.ready {
					if !ready {
						target = t
						block = n
						break
					}
				}
				if target != nil {
					break
				}
			}
		}
		s.mu.Unlock()
		if target == nil {
			// Preserve the completed cache while releasing the drive and helper.
			r.Close()
			r = nil
			<-s.ctx.Done()
			return
		}
		start := target.layout.Start + block*blockSectors
		count := min(blockSectors, target.layout.End-start)
		var data []byte
		for len(data) < count*sectorBytes {
			part, e := r.Read(start+len(data)/sectorBytes, count-len(data)/sectorBytes)
			if e != nil {
				err = e
				break
			}
			if len(part) == 0 || len(part)%sectorBytes != 0 || len(part) > count*sectorBytes-len(data) {
				err = fmt.Errorf("invalid audio read size")
				break
			}
			data = append(data, part...)
		}
		if err == nil {
			_, err = target.file.WriteAt(data, 44+int64(block*blockSectors*sectorBytes))
		}
		if err != nil {
			s.fail(fmt.Errorf("cache CD track %d: %w", target.layout.Number, err))
			<-s.ctx.Done()
			return
		}
		s.mu.Lock()
		target.ready[block] = true
		s.cached += int64(len(data))
		s.signal()
		s.mu.Unlock()
	}
}
func wavHeader(size uint32) []byte {
	b := make([]byte, 44)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], size+36)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 2)
	binary.LittleEndian.PutUint32(b[24:], 44100)
	binary.LittleEndian.PutUint32(b[28:], 176400)
	binary.LittleEndian.PutUint16(b[32:], 4)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], size)
	return b
}
func (c *Cache) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "audio" || (r.Method != "GET" && r.Method != "HEAD") {
		http.NotFound(w, r)
		return
	}
	c.mu.Lock()
	s := c.session
	c.mu.Unlock()
	if s == nil || s.token != parts[1] || s.ctx.Err() != nil {
		http.NotFound(w, r)
		return
	}
	number, err := strconv.Atoi(strings.TrimSuffix(parts[2], ".wav"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var t *track
	for _, candidate := range s.tracks {
		if candidate.layout.Number == number {
			t = candidate
			break
		}
	}
	if t == nil {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	readError := s.err
	complete := true
	for _, ready := range t.ready {
		if !ready {
			complete = false
			break
		}
	}
	s.mu.Unlock()
	if readError != nil && !complete {
		http.Error(w, "CD reader unavailable: "+readError.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, parts[2], time.Time{}, &cachedReader{ctx: r.Context(), session: s, track: t})
}

type cachedReader struct {
	ctx     context.Context
	session *session
	track   *track
	offset  int64
}

func (r *cachedReader) Seek(offset int64, whence int) (int64, error) {
	size := int64(r.track.layout.End-r.track.layout.Start)*sectorBytes + 44
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.offset
	case io.SeekEnd:
		offset += size
	default:
		return 0, fmt.Errorf("invalid seek")
	}
	if offset < 0 {
		return 0, fmt.Errorf("negative seek")
	}
	r.offset = offset
	return offset, nil
}
func (r *cachedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	size := int64(r.track.layout.End-r.track.layout.Start)*sectorBytes + 44
	if r.offset >= size {
		return 0, io.EOF
	}
	s, t := r.session, r.track
	if r.offset < 44 {
		p = p[:min(len(p), int(44-r.offset))]
	} else {
		block := int((r.offset - 44) / (blockSectors * sectorBytes))
		for {
			if err := r.ctx.Err(); err != nil {
				return 0, err
			}
			if err := s.ctx.Err(); err != nil {
				return 0, err
			}
			s.mu.Lock()
			ready, err, changed := t.ready[block], s.err, s.changed
			if !ready && err == nil {
				s.demand[t] = block
			}
			s.mu.Unlock()
			if ready {
				break
			}
			if err != nil {
				return 0, err
			}
			select {
			case <-r.ctx.Done():
				return 0, r.ctx.Err()
			case <-s.ctx.Done():
				return 0, s.ctx.Err()
			case <-changed:
			}
		}
		end := min(size, 44+int64((block+1)*blockSectors*sectorBytes))
		p = p[:min(len(p), int(end-r.offset))]
	}
	n, err := t.file.ReadAt(p, r.offset)
	r.offset += int64(n)
	return n, err
}

// InvalidateUnless lets the drive monitor stop serving removed media even if
// the main controller is currently waiting for an MPD acknowledgement.
func (c *Cache) InvalidateUnless(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil && c.session.id != id {
		c.session.cancel()
		c.session = nil
	}
}
