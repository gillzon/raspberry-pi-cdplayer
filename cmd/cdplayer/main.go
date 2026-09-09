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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gillzon/raspberry-pi-cdplayer/internal/audio"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/disc"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/library"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/metadata"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/mpd"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/player"
	"github.com/gillzon/raspberry-pi-cdplayer/internal/radio"
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
	address := flag.String("mpd", "127.0.0.1:6600", "MPD TCP address (use port 6601 for the optional dedicated instance)")
	checkAudio := flag.Bool("check-audio", false, "check persistent CD reader dependencies without opening the drive")
	cachedAudio := flag.Bool("audio-cache", true, "read CD once in the background and serve cached WAV audio to local MPD")
	verifyAudio := flag.Bool("audio-verify", false, "enable slower software audio verification and repair for difficult discs")
	cdSpeed := flag.Int("cd-speed", 0, "request CD read speed multiplier for cached audio, e.g. 4 (0 leaves drive default; not a current limit)")
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
	musicDir := flag.String("music-dir", envDefault("CDPLAYER_MUSIC_DIR", "/media/cdplayer"), "mounted USB music directory (empty disables USB library)")
	flag.Parse()
	if *cdSpeed < 0 || int64(*cdSpeed) > 2147483647 {
		slog.Error("cd-speed must be a nonnegative 32-bit integer")
		os.Exit(2)
	}
	if *checkAudio {
		if err := audio.Check(); err != nil {
			slog.Error("audio dependencies", "error", err)
			os.Exit(1)
		}
		fmt.Println("Persistent CD reader dependencies available")
		return
	}
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
	outputPath := ""
	if *cacheDir != "" {
		outputPath = filepath.Join(*cacheDir, "audio-output")
	}
	if outputPath != "" {
		saved, err := mpd.LoadOutput(outputPath)
		if err != nil {
			slog.Warn("load audio output setting", "error", err)
		}
		backend.PreferredOutput = saved
		if device := mpd.OutputDevice(saved); device != "" {
			receiver.Device = device
		}
	}
	var outputs []mpd.Output
	if *autoDevice {
		slog.Warn("MPD will select its own CD drive; connect only one CD drive", "detected_device", *device)
	}
	defer backend.Close()
	selectedDevice := *device
	var rawDrive interface {
		Read() (disc.Disc, error)
		Eject() error
		DevicePath() string
	} = &disc.Drive{Device: *device}
	if *device == "auto" {
		rawDrive = &disc.AutoDrive{}
	}
	drive := disc.NewMonitor(ctx, rawDrive, *poll)
	controller := &player.Controller{Drive: drive, Backend: backend}
	var audioCache *audio.Cache
	audioReady := make(chan struct{}, 1)
	if *cachedAudio {
		host, _, err := net.SplitHostPort(*address)
		if err != nil || (host != "localhost" && !net.ParseIP(host).IsLoopback()) {
			slog.Error("cached audio requires MPD on this Pi; use -audio-cache=false for remote MPD")
			return
		}
		audioListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			slog.Error("start local audio server", "error", err)
			return
		}
		root := ""
		if *cacheDir != "" {
			root = filepath.Join(*cacheDir, "audio")
		}
		audioCache = &audio.Cache{Root: root, BaseURL: "http://" + audioListener.Addr().String(), Open: audio.ReaderWithOptions(*verifyAudio, *cdSpeed)}
		audioCache.ReadyNotify = audioReady
		if err := audioCache.Init(); err != nil {
			audioListener.Close()
			slog.Error("initialize audio cache", "error", err)
			return
		}
		defer audioCache.Shutdown()
		// Audio responses can last a whole track and must not use UI write deadlines.
		audioServer := &http.Server{Handler: audioCache, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
		defer audioServer.Close()
		go func() {
			if err := audioServer.Serve(audioListener); err != nil && err != http.ErrServerClosed {
				slog.Error("local audio server stopped", "error", err)
				cancel()
			}
		}()
		backend.TrackURL = audioCache.TrackURL
		controller.Prepare = func(d disc.Disc) error {
			layout := make([]audio.Layout, 0, len(d.Layout))
			for _, t := range d.Layout {
				layout = append(layout, audio.Layout{Number: t.Number, Start: t.Start, End: t.End})
			}
			if err := audioCache.Observe(d.ID, drive.DevicePath(), layout); err != nil {
				return err
			}
			return audioCache.Ready()
		}
		controller.Release = audioCache.Close
		drive.SetObserver(func(d disc.Disc, err error) {
			if err == nil {
				audioCache.InvalidateUnless(d.ID)
			}
		})
	}
	trackRequests := player.NewTrackRequests()
	if audioCache != nil {
		trackRequests.OnSelect = audioCache.Select
	}
	var music *library.Library
	var usbQueue []library.Track
	usbMix := false
	if *musicDir != "" {
		if *cacheDir == "" {
			slog.Error("USB music requires -cache-dir; use -music-dir= to disable")
			return
		}
		var err error
		music, err = library.New(*musicDir, filepath.Join(*cacheDir, "library.sqlite"))
		if err != nil {
			slog.Error("initialize USB library", "error", err)
			return
		}
		host, _, err := net.SplitHostPort(*address)
		if err != nil || (host != "localhost" && !net.ParseIP(host).IsLoopback()) {
			slog.Error("USB music requires MPD on this Pi; use -music-dir= for remote MPD")
			return
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			slog.Error("USB audio server", "error", err)
			return
		}
		music.BaseURL = "http://" + listener.Addr().String()
		server := &http.Server{Handler: music, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
		defer server.Close()
		go func() {
			if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
				slog.Error("USB audio server", "error", err)
				cancel()
			}
		}()
		go music.Run(ctx)
	}
	commands := make(chan controlRequest)
	website := &web.Server{Library: music, Selection: trackRequests.Snapshot, Control: func(ctx context.Context, cmd web.Command) error {
		if cmd.Action == "track" {
			if err := trackRequests.Submit(cmd.Track, cmd.DiscID); err != nil {
				return err
			}

			return nil
		}
		if cmd.Action == "play" || cmd.Action == "pause" || cmd.Action == "stop" || cmd.Action == "eject" || cmd.Action == "source-cd" || cmd.Action == "usb-play" || cmd.Action == "usb-mix" {
			trackRequests.Cancel()
		}
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
		server := &http.Server{Handler: website.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 130 * time.Second, IdleTimeout: 60 * time.Second}
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
	selectedStation := radio.Stations()[0]
	lastError := ""
	publish := func(err error) {
		trackRequests.Update(controller.Disc().ID, controller.Disc().Tracks, controller.Source() == "cd")
		albums.Observe(ctx, controller.Disc())
		selectedDevice = drive.DevicePath()
		state := web.State{Outputs: outputs, Source: controller.Source(), Spotify: receiver.State, Device: selectedDevice, Disc: controller.Disc(), MPD: backend.Status(), Updated: time.Now()}
		if controller.Source() == "radio" {
			station := selectedStation
			state.Radio = &station
		}
		if controller.Source() == "usb" {
			state.USBQueueLength = len(usbQueue)
			state.USBMix = usbMix
			if position, e := strconv.Atoi(state.MPD["song"]); e == nil && position >= 0 && position < len(usbQueue) {
				song := usbQueue[position]
				song.Path = ""
				state.USB = &song
			}
		}
		if audioCache != nil && controller.Source() == "cd" {
			state.Audio = audioCache.Status()
			if state.Audio.Error != "" && err == nil {
				state.Error = state.Audio.Error
			}
		}
		if err != nil {
			state.Error = err.Error()
		}
		website.Set(state)
	}
	startRadio := func(ctx context.Context, station radio.Station) error {
		if err := receiver.Stop(); err != nil {
			return err
		}
		controller.UseRadio()
		selectedStation = station
		defer receiver.Tick()
		if _, err := backend.Connect(ctx); err != nil {
			return err
		}
		return backend.StartURLs([]string{station.URL}, 0)
	}
	handleControl := func(request controlRequest) error {
		if request.command.Action == "source-next" {
			switch controller.Source() {
			case "cd", "idle":
				request.command.Action = "source-usb"
			case "usb":
				request.command.Action = "source-radio"
			case "radio":
				if receiver.State.Enabled {
					request.command.Action = "source-spotify"
				} else {
					request.command.Action = "source-cd"
				}
			default:
				request.command.Action = "source-cd"
			}
		}
		if request.command.Action == "toggle" {
			request.command.Action = "play"
			if backend.Status()["state"] == "play" {
				request.command.Action = "pause"
			}
		}
		if request.command.Action == "play" && controller.Source() == "usb" && len(usbQueue) == 0 {
			request.command.Action = "usb-mix"
		}

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
			case "source-usb":
				if err = receiver.Stop(); err != nil {
					break
				}
				if _, err = backend.Connect(request.ctx); err != nil {
					break
				}
				if err = backend.Clear(); err != nil {
					break
				}
				controller.UseUSB()
				usbQueue = nil
				usbMix = false
				receiver.Tick()
			case "source-radio", "radio-play":
				station := selectedStation
				if request.command.Action == "radio-play" {
					var ok bool
					station, ok = radio.Find(request.command.Station)
					if !ok {
						err = fmt.Errorf("unknown radio station")
						break
					}
				}
				err = startRadio(request.ctx, station)
			case "source-spotify":
				if !receiver.State.Enabled {
					err = fmt.Errorf("Spotify is disabled")
					break
				}
				err = controller.UseSpotify(request.ctx)
				receiver.Tick()
			case "outputs":
				_, err = backend.Connect(request.ctx)
				if err == nil {
					outputs, err = backend.Outputs()
				}
			case "output":
				_, err = backend.Connect(request.ctx)
				if err == nil {
					outputs, err = backend.Outputs()
				}
				var chosen *mpd.Output
				for i := range outputs {
					if outputs[i].Name == request.command.Output {
						chosen = &outputs[i]
					}
				}
				if err == nil && chosen == nil {
					err = fmt.Errorf("output is no longer configured; refresh Settings")
				}
				if err == nil && outputPath == "" {
					err = fmt.Errorf("set a cache directory to save audio settings")
				}
				if err == nil {
					err = receiver.Stop()
					if err == nil {
						controller.SpotifyDisconnected()
						err = backend.SelectOutput(chosen.Name)
					}
					if err == nil {
						backend.PreferredOutput = chosen.Name
						if chosen.Device != "" {
							receiver.Device = chosen.Device
						}
						if e := mpd.SaveOutput(outputPath, chosen.Name); e != nil {
							err = fmt.Errorf("output changed but could not save for reboot: %w", e)
						}
					}
					receiver.Tick()
					if updated, e := backend.Outputs(); e == nil {
						outputs = updated
					}
				}
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
			case "usb-play", "usb-mix":
				if music == nil {
					err = fmt.Errorf("USB music is disabled")
					break
				}
				var queue []library.Track
				selected := -1
				mix := request.command.Action == "usb-mix"
				if mix {
					queue, err = music.Mix(request.ctx)
					selected = 0
				} else {
					var song library.Track
					song, err = music.Get(request.ctx, request.command.SongID)
					if err != nil {
						break
					}
					file, e := music.Open(song)
					if e != nil {
						err = e
						break
					}
					file.Close()
					queue, err = music.Album(request.ctx, song.ID)
					for i, t := range queue {
						if t.ID == song.ID {
							selected = i
						}
					}
				}
				if err != nil {
					break
				}
				urls := make([]string, len(queue))
				for i, t := range queue {
					urls[i] = music.URL(t.ID)
				}

				if selected < 0 {
					err = fmt.Errorf("song changed; refresh the library")
					break
				}
				if err = receiver.Stop(); err != nil {
					break
				}
				controller.UseUSB()
				usbQueue = nil
				_, err = backend.Connect(request.ctx)
				if err == nil {
					err = backend.StartURLs(urls, selected)
				}
				if err == nil {
					usbQueue = queue
					usbMix = mix
				}
				receiver.Tick()
			case "source-cd":
				err = receiver.Stop()
				if err == nil && (controller.Source() == "usb" || controller.Source() == "radio") {
					// Stop USB even when a missing/unreadable CD prevents the next Step.
					err = backend.Clear()
				}
				if err == nil {
					controller.UseCD()
					receiver.Tick()
				}
			case "eject":
				err = controller.Eject()
			default:
				if controller.Source() == "radio" {
					switch request.command.Action {
					case "next":
						err = startRadio(request.ctx, radio.Next(selectedStation.ID, 1))
					case "previous":
						err = startRadio(request.ctx, radio.Next(selectedStation.ID, -1))
					case "play":
						err = startRadio(request.ctx, selectedStation)
					case "pause", "stop":
						err = backend.Control("stop", 0)
					default:
						err = fmt.Errorf("unsupported radio control")
					}
				} else if controller.Source() == "usb" {
					if request.command.Action == "track" {
						err = fmt.Errorf("select a USB song from Pick song")
					} else {
						err = backend.Control(request.command.Action, position)
					}
				} else if controller.Source() == "idle" && request.command.Action == "play" {
					if err = receiver.Stop(); err == nil {
						controller.UseCD()
						receiver.Tick()
					}
				} else if controller.Source() != "cd" {
					err = fmt.Errorf("select Switch to CD first")
				} else if audioCache != nil && request.command.Action == "play" && audioCache.Status().Error != "" {
					// Manual Play retries the reader instead of replaying broken URLs.
					audioCache.Close()
					controller.UseCD()
				} else if audioCache != nil && request.command.Action != "stop" && audioCache.Ready() != nil {
					err = audioCache.Ready()
				} else {

					err = backend.Control(request.command.Action, position)
				}
			}
		}
		// Acknowledge the control as soon as MPD acknowledges it. Playback
		// health is refreshed by Step; a slow status reply must not turn
		// an already accepted track change into a failed web command.
		publish(err)
		if err != nil {
			slog.Warn("web playback command failed", "action", request.command.Action, "error", err)
		}
		return err
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
			request.result <- handleControl(request)
		case <-trackRequests.Wake:
			if selection := trackRequests.Take(); selection != nil {
				err := handleControl(controlRequest{ctx: ctx, command: web.Command{Action: "track", Track: selection.Track, DiscID: selection.DiscID}})
				trackRequests.Complete(selection, err)
				publish(err)
			}
		case <-ctx.Done():
			if audioCache != nil {
				audioCache.Close()
			}
			if err := backend.Clear(); err != nil {
				slog.Warn("could not stop MPD on shutdown", "error", err)
			}
			return
		case <-drive.Updates:
		case <-audioReady:
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
