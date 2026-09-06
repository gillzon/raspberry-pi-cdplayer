package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/metadata"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/mpd"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/player"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/spotify"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/systeminfo"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/web"
)

type controlRequest struct {
	ctx     context.Context
	command web.Command
	result  chan error
}

func main() {
	device := flag.String("device", "auto", "CD drive: auto detects the single connected drive, or an absolute /dev path")
	address := flag.String("mpd", "127.0.0.1:6601", "dedicated MPD TCP address")
	autoDevice := flag.Bool("mpd-auto-device", false, "let MPD select the CD drive (single-drive workaround for Bad track number)")
	poll := flag.Duration("poll", time.Second, "disc polling interval")
	httpAddress := flag.String("http", ":8080", "web interface address (empty disables it)")
	defaultCache := ""
	if path, err := os.UserCacheDir(); err == nil {
		defaultCache = filepath.Join(path, "cdplayer")
	}
	cacheDir := flag.String("cache-dir", defaultCache, "album and artwork cache directory (empty disables disk caching)")
	spotifyEnabled := flag.Bool("spotify", os.Getenv("CDPLAYER_SPOTIFY_ENABLED") == "1", "enable Spotify Connect (requires librespot and web interface)")
	spotifyDevice := flag.String("spotify-device", envDefault("CDPLAYER_SPOTIFY_DEVICE", "plughw:CARD=Headphones,DEV=0"), "Spotify ALSA output")
	spotifyName := flag.String("spotify-name", envDefault("CDPLAYER_SPOTIFY_NAME", "Raspberry Pi CD Player"), "Spotify Connect device name")
	spotifyEvent := flag.Bool("spotify-event", false, "internal Spotify sink handoff hook")
	flag.Parse()
	if *spotifyEvent {
		if err := spotify.Hook(); err != nil {
			slog.Error("Spotify handoff", "error", err)
			os.Exit(1)
		}
		return
	}
	if *spotifyEnabled && *httpAddress == "" {
		slog.Error("Spotify requires the web interface")
		os.Exit(2)
	}
	receiver := &spotify.Manager{Binary: envDefault("CDPLAYER_SPOTIFY_BINARY", "librespot"), Device: *spotifyDevice, State: spotify.State{Enabled: *spotifyEnabled, Name: *spotifyName}}
	defer receiver.Stop()
	if *poll < 100*time.Millisecond || (*device != "auto" && !strings.HasPrefix(filepath.Clean(*device), "/dev/")) || strings.ContainsAny(*device, "\r\n\"\\") || flag.NArg() != 0 {
		slog.Error("use auto or a /dev/ device path, no positional arguments, and a poll interval of at least 100ms")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	backend := &mpd.Client{Address: *address, Device: filepath.Clean(*device), AutoDevice: *autoDevice || *device == "auto"}
	if *autoDevice {
		slog.Warn("MPD will select its own CD drive; connect only one CD drive", "detected_device", *device)
	}
	defer backend.Close()
	selectedDevice := *device
	var drive player.Drive = &disc.Drive{Device: *device}
	if *device == "auto" {
		selectedDevice = ""
		drive = &disc.AutoDrive{OnChange: func(path string) {
			selectedDevice = path
			backend.Device = path
			slog.Info("CD drive selection changed", "device", path)
		}}
	}
	controller := &player.Controller{Drive: drive, Backend: backend}
	commands := make(chan controlRequest)
	website := &web.Server{Control: func(ctx context.Context, cmd web.Command) error {
		request := controlRequest{ctx: ctx, command: cmd, result: make(chan error, 1)}
		select {
		case commands <- request:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-request.result:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	website.Set(web.State{Device: *device, Error: "Starting player"})
	albums := metadata.New(*cacheDir)
	website.Metadata = albums
	go albums.Run(ctx)
	monitor := systeminfo.New()
	website.System = monitor.Snapshot
	go monitor.Run(ctx)
	if *httpAddress != "" {
		listener, err := net.Listen("tcp", *httpAddress)
		if err != nil {
			slog.Error("start web interface", "error", err)
			return
		}
		host, port, _ := net.SplitHostPort(listener.Addr().String())
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			if ip.To4() != nil {
				host = "127.0.0.1"
			} else {
				host = "::1"
			}
		}
		receiver.Callback = "http://" + net.JoinHostPort(host, port) + "/api/control"
		server := &http.Server{Handler: website.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
		defer server.Close()
		go func() {
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				slog.Error("web interface stopped", "error", err)
				cancel()
			}
		}()
		slog.Info("web interface listening", "address", listener.Addr())
	}
	ticker := time.NewTicker(*poll)
	defer ticker.Stop()
	slog.Info("CD player starting", "device", *device, "mpd", *address)
	lastError := ""
	publish := func(err error) {
		albums.Observe(ctx, controller.Disc())
		state := web.State{Source: controller.Source(), Spotify: receiver.State, Device: selectedDevice, Disc: controller.Disc(), MPD: backend.Status(), Updated: time.Now()}
		if err != nil {
			state.Error = err.Error()
		}
		website.Set(state)
	}
	for {
		receiver.Tick()
		if !receiver.State.Running {
			controller.SpotifyDisconnected()
		}
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
		publish(err)
		select {
		case request := <-commands:
			err := request.ctx.Err()
			position := -1
			if err == nil && request.command.DiscID != "" && request.command.DiscID != controller.Disc().ID {
				err = fmt.Errorf("disc changed; select a track on the current disc")
			}
			if err == nil && request.command.Action == "track" {
				position = slices.Index(controller.Disc().Tracks, request.command.Track)
				if position < 0 {
					err = fmt.Errorf("track is not on the current disc")
				}
			}
			if err == nil {
				switch request.command.Action {
				case "spotify-event":
					if !receiver.Accept(request.command.Token) {
						err = fmt.Errorf("obsolete Spotify receiver")
					} else {
						receiver.Apply(request.command.Event)
						if request.command.Event.Kind == "session_disconnected" {
							controller.SpotifyDisconnected()
						}
					}
				case "spotify-start":
					if !receiver.Accept(request.command.Token) {
						err = fmt.Errorf("obsolete Spotify receiver")
					} else {
						err = controller.UseSpotify(request.ctx)
						if err == nil && request.command.Event.Kind == "session_connected" {
							receiver.Apply(request.command.Event)
						}
					}
				case "source-cd":
					if err = receiver.Stop(); err == nil {
						controller.UseCD()
						receiver.Tick()
					}
				case "eject":
					err = controller.Eject()
				default:
					if controller.Source() == "idle" && request.command.Action == "play" {
						if err = receiver.Stop(); err == nil {
							controller.UseCD()
							receiver.Tick()
						}
					} else if controller.Source() != "cd" {
						err = fmt.Errorf("select Switch to CD first")
					} else {
						err = backend.Control(request.command.Action, position)
					}
				}
			}
			if err == nil && controller.Source() == "cd" {
				err = backend.PlaybackError()
			}
			publish(err)
			if err != nil {
				slog.Warn("web playback command failed", "action", request.command.Action, "error", err)
			}
			request.result <- err
		case <-ctx.Done():
			if err := backend.Clear(); err != nil {
				slog.Warn("could not stop MPD on shutdown", "error", err)
			}
			return
		case <-ticker.C:
		}
	}
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
