# Raspberry Pi CD player

Milestone 1: insert an audio CD and play it automatically on a Raspberry Pi 4.
Go detects discs using Linux CD-ROM ioctls; a dedicated Music Player Daemon
(MPD) instance reads the audio and sends it to ALSA. No internet connection,
ripping, metadata service, or mounted filesystem is needed for playback.

## Behavior

- Poll the drive once a second, including immediately on startup.
- Queue each audio track in disc order and request playback once the disc is readable.
- Skip data tracks on mixed-mode CDs; ignore data-only CDs with a readable TOC.
- Stop and clear the queue when removal or drive disconnection is detected.
- Play the same CD again after an observed removal and reinsertion.
- Retry drive readiness, connection, and queue setup failures.
- Restore playback from the first audio track after an MPD reconnection.
- Preserve manual pause/stop and stop at the end of the album without looping.
- Report asynchronous playback failures, such as read errors or unavailable audio
  output. These do not trigger an endless restart loop; fix the cause and use
  `mpc play`, reinsert the disc, or restart the service.

Spin-up and table-of-contents reading add hardware-dependent delay. There is no
fixed insertion-to-sound guarantee yet. A removal/reinsertion entirely between
polls can be missed. The table of contents is cached while the drive remains
ready, so routine polls only check drive status instead of reading every track
again during audio extraction. Removal, disconnection, or a not-ready state
invalidates the cache.

The screen, artwork, track titles, physical buttons, and ripping are later milestones.

## Hardware and OS

- Raspberry Pi 4 running Raspberry Pi OS (64-bit recommended).
- USB CD/DVD drive that supports digital audio extraction; enough USB power for
  the drive, using its own supply or a powered hub if required.
- Wired audio output to powered speakers or an amplifier (analog, USB DAC, or HDMI).
- Go 1.22 or newer for building; no Go runtime or compiler is needed on the Pi
  if you copy in a cross-compiled binary.

## 1. Install and check the playback engine on the Pi

```sh
sudo apt update
sudo apt install mpd mpc eject alsa-utils
mpd --version
ls -l /dev/sr*
aplay -l
```

In `mpd --version`, confirm that the input plugins include **cdio_paranoia**.
An MPD build without this plugin cannot play CDs; install or build one with
libcdio-paranoia support before proceeding. The drive defaults to `/dev/sr0`.

This project uses its own MPD instance on **127.0.0.1:6601**. It owns that
instance's queue and playback settings. An existing MPD on port 6600 can remain
installed, but stop any other player using the same drive or audio output while
testing. Disable desktop CD autoplay if it interferes.

## 2. Build

From this repository, either build directly on the Pi:

```sh
go test ./...
go build -o bin/cdplayer ./cmd/cdplayer
```

Or cross-compile on your development computer for 64-bit Raspberry Pi OS:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/cdplayer ./cmd/cdplayer
```

For 32-bit Raspberry Pi OS, use `GOARCH=arm GOARM=7` instead. Copy the binary
and the `deploy/` directory to the Pi before continuing. The Go application has
no external Go dependencies; MPD and its CD libraries run separately on the Pi.

## 3. Install on the Pi

Run from the directory containing `bin/` and `deploy/`. Create the service
account once (skip `useradd` on subsequent installations):

```sh
sudo useradd --system --user-group --no-create-home --shell /usr/sbin/nologin cdplayer
sudo install -m 0755 bin/cdplayer /usr/local/bin/cdplayer
sudo install -d -m 0755 /etc/cdplayer
sudo install -m 0644 deploy/mpd.conf /etc/cdplayer/mpd.conf
sudo install -m 0644 deploy/cdplayer-mpd.service /etc/systemd/system/cdplayer-mpd.service
sudo install -m 0644 deploy/cdplayer.service /etc/systemd/system/cdplayer.service
```

Edit `/etc/cdplayer/mpd.conf` to select an ALSA output. `device "default"` is
the starting configuration, but an unattended system service may need an
explicit hardware device. Use the card name from `aplay -l`, for example
`device "plughw:CARD=Headphones,DEV=0"` **only if that card exists**. The service
does not use a desktop user's PulseAudio/PipeWire session. Start with the
amplifier/speakers at a comfortable low volume.

If your drive is not `/dev/sr0`, change `-device` in
`/etc/systemd/system/cdplayer.service`. A stable symlink under `/dev/disk/by-id/`
can also be used if available for your drive.

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now cdplayer-mpd.service cdplayer.service
sudo journalctl -u cdplayer -u cdplayer-mpd -f
```

Insert an audio CD. The log should show `CD playback requested`; verify actual
sound and advancing elapsed time with:

```sh
mpc -p 6601 status
mpc -p 6601 playlist
```

Both services run as `cdplayer`, with CD-ROM permissions and, for MPD, audio
permissions. They start without a network dependency and retry when the drive
or MPD is unavailable. Logs go to the journal; MPD stores its database under
`/var/lib/cdplayer-mpd`. No queue state file is configured.

## Controls for the playback proof

```sh
mpc -p 6601 toggle
mpc -p 6601 next
mpc -p 6601 prev
mpc -p 6601 stop
mpc -p 6601 play
```

Use the drive's eject button. If the drive locks its tray during playback,
stop it first, then eject:

```sh
mpc -p 6601 stop
sudo eject /dev/sr0
```

Leave the disc out for at least one poll before reinserting it. Changing tracks
uses separate MPD CD URIs; seamless/gapless transitions are not yet guaranteed.

To run the Go service in the foreground for debugging, first stop its systemd
instance so two controllers do not compete:

```sh
sudo systemctl stop cdplayer
sudo -u cdplayer -g cdrom /usr/local/bin/cdplayer -device /dev/sr0 -mpd 127.0.0.1:6601 -poll 1s
```

Press Ctrl+C to stop and clear playback. Afterwards, run
`sudo systemctl start cdplayer` to restore the boot-managed service.

## Hardware acceptance checklist

These checks require the actual Pi, drive, and audio output. Automated tests do
not establish that the hardware works.

1. Boot with an empty drive, insert a known-good audio CD, and record the time
   from tray closure to sound. Check that track 1 starts without a command.
2. Listen across a natural track boundary and verify the playlist order. Use
   `mpc -p 6601 next` as an additional control check.
3. Pause for several polls, then resume. Playback must not restart on its own.
4. Eject during playback (stop first if the tray is locked), wait two seconds,
   and reinsert the same disc. Playback should start from the first audio track.
5. Disconnect Wi-Fi/Ethernet and repeat insertion. Audio should behave the same.
6. Reboot with a disc already inserted. Playback should start automatically
   after the services and drive are ready.
7. Let the last track finish. It should remain stopped through subsequent polls.
8. Restart `cdplayer-mpd` with a disc inserted. The Go service should reconnect
   and restart from the first audio track.
9. Try a data disc or unreadable disc, then replace it with a known-good audio
   CD. Inspect errors and verify recovery without rebooting.

If there is no sound, check both service logs and `mpc -p 6601 status`. Verify
the `cdio_paranoia` plugin, drive permissions/power, ALSA card name, and that
another process has not taken the output. An MPD `play` acknowledgment alone
does not prove the audio output opened successfully.

### Slow startup

Keep the app running before inserting a disc; for everyday use, build the
binary and enable the systemd services. `go run` also builds the program, but
that only affects app launch, not subsequent disc insertions.

Logs distinguish `CD playback requested` (MPD accepted the command) from
`MPD playback progressing` (MPD reports advancing audio time). Neither proves
that speakers are audible. The request log includes drive probe and queue setup
durations, and probes taking a second or longer are logged separately.

A minute-long stall with MPD disconnections is a fault to diagnose, not an
intentional startup delay. After reproducing it on the Pi, collect:

```sh
sudo journalctl -u mpd -b -n 60 --no-pager
sudo journalctl -k -b -n 80 --no-pager
```

For the dedicated instance, replace `-u mpd` with `-u cdplayer-mpd`. These logs
help distinguish MPD failures from USB resets and drive I/O timeouts. Drive
spin-up still takes time; software cannot eliminate that mechanical delay.

### MPD reports `Bad track number`

Some MPD releases (confirmed in 0.24.5) incorrectly split a CD URI at the
first slash. A valid URI such as `cdda:///dev/sr0/1` then fails before the drive
is opened. With **only one CD drive connected**, run:

```sh
go run ./cmd/cdplayer -mpd 127.0.0.1:6600 -mpd-auto-device
```

Use port `6601` for the project's dedicated MPD instance. This option generates
`cdda:///1`, `cdda:///2`, etc., letting MPD discover the audio drive. Go still
monitors the path given by `-device`. With multiple drives, MPD could select a
different drive; use an MPD version with the corrected parser and omit this
option instead. For an installed service, append `-mpd-auto-device` to its
`ExecStart`, then run `sudo systemctl daemon-reload` and restart `cdplayer`.

Parser sources: [MPD 0.24.5](https://github.com/MusicPlayerDaemon/MPD/blob/v0.24.5/src/input/plugins/CdioParanoiaInputPlugin.cxx),
[current upstream](https://github.com/MusicPlayerDaemon/MPD/blob/master/src/input/plugins/CdioParanoiaInputPlugin.cxx).

## Development checks

```sh
go test -race ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/cdplayer-arm64 ./cmd/cdplayer
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o bin/cdplayer-armv7 ./cmd/cdplayer
```

Controller tests cover insertion, startup with a disc, removal/reinsertion,
data tracks, retry behavior, MPD restart, and avoiding unwanted replay. MPD
protocol tests use an in-memory fake server to check queue commands and error
handling. Native Linux tests also check missing/invalid drive paths.

References: [MPD CD playback plugin](https://mpd.readthedocs.io/en/stable/plugins.html#cdio-paranoia),
[MPD protocol](https://mpd.readthedocs.io/en/stable/protocol.html),
[Linux CD-ROM ABI](https://github.com/torvalds/linux/blob/master/include/uapi/linux/cdrom.h).
