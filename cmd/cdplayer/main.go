package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/mpd"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/player"
)

func main() {
	device := flag.String("device", "/dev/sr0", "CD drive device (absolute /dev path)")
	address := flag.String("mpd", "127.0.0.1:6601", "dedicated MPD TCP address")
	poll := flag.Duration("poll", time.Second, "disc polling interval")
	flag.Parse()
	if *poll < 100*time.Millisecond || !strings.HasPrefix(filepath.Clean(*device), "/dev/") || strings.ContainsAny(*device, "\r\n\"\\") || flag.NArg() != 0 {
		slog.Error("use a /dev/ device path, no positional arguments, and a poll interval of at least 100ms")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	backend := &mpd.Client{Address: *address, Device: filepath.Clean(*device)}
	defer backend.Close()
	controller := &player.Controller{Drive: disc.Drive{Device: *device}, Backend: backend}
	ticker := time.NewTicker(*poll)
	defer ticker.Stop()
	slog.Info("CD player starting", "device", *device, "mpd", *address)
	lastError := ""
	for {
		err := controller.Step(ctx)
		if err != nil {
			if err.Error() != lastError {
				slog.Error("CD player", "error", err)
				lastError = err.Error()
			}
		} else if lastError != "" {
			slog.Info("CD player recovered")
			lastError = ""
		}
		select {
		case <-ctx.Done():
			if err := backend.Clear(); err != nil {
				slog.Warn("could not stop MPD on shutdown", "error", err)
			}
			return
		case <-ticker.C:
		}
	}
}
