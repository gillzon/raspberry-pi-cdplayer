package audio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Init reserves the cache directory for this process and removes only obsolete
// session directories. It must run before Observe or serving HTTP requests.
func (c *Cache) Init() error {
	if c.Root == "" {
		root, err := os.MkdirTemp("", "cdplayer-audio-")
		if err != nil {
			return err
		}
		c.Root = root
		c.temporaryRoot = true
	}
	if err := os.MkdirAll(c.Root, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(c.Root, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = lockCache(file); err != nil {
		file.Close()
		return fmt.Errorf("audio cache already in use or cannot be locked: %w", err)
	}
	c.lock = file
	entries, err := os.ReadDir(c.Root)
	if err != nil {
		file.Close()
		c.lock = nil
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "cd-audio-") {
			continue
		}
		if err = os.RemoveAll(filepath.Join(c.Root, entry.Name())); err != nil {
			file.Close()
			c.lock = nil
			return err
		}
	}
	return nil
}
func (c *Cache) Shutdown() {
	c.Close()
	done := make(chan struct{})
	go func() {
		c.workers.Wait()
		if c.lock != nil {
			c.lock.Close()
		}
		if c.temporaryRoot {
			os.RemoveAll(c.Root)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}
