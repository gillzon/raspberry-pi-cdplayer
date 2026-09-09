# Raspberry Pi CD player

Insert an audio CD and play it automatically on a Raspberry Pi 4.
Go detects discs using Linux CD-ROM ioctls. One background libcdio reader keeps
the drive open, reads ahead into a temporary WAV cache, and serves audio to
Music Player Daemon (MPD) over localhost. MPD sends it to ALSA. Playback starts
before the full track is cached; no internet or mounted CD filesystem is needed.

## Behavior

- Poll the drive once a second, including immediately on startup.
- Queue each audio track in disc order and request playback once the disc is readable.
- Skip data tracks on mixed-mode CDs; ignore data-only CDs with a readable TOC.
- Stop and clear the queue when removal or drive disconnection is detected.
- Play the same CD again after an observed removal and reinsertion.
- Retry drive readiness, connection, and queue setup failures.
- Preserve the current track and play/pause/stop state on an MPD reconnection
  when the existing queue matches the current disc; rebuild a missing queue.
- Preserve manual pause/stop and stop at the end of the album without looping.
- Report asynchronous playback failures, such as read errors or unavailable audio
  output. These do not trigger an endless restart loop; fix the cause and use
  Play in the web UI, reinsert the disc, or restart the service. Web Play also
  retries a failed audio-cache reader.

Cached playback waits for the reader to validate the disc layout and produce
audio before queuing tracks. If startup fails, the UI reports the helper's
diagnostic and exit status. A `layout mismatch` includes the track number and
expected/actual sector boundaries; include that full message when reporting it.

Spin-up and table-of-contents reading add hardware-dependent delay. There is no
fixed insertion-to-sound guarantee yet. A removal/reinsertion entirely between
polls can be missed. The table of contents is cached while the drive remains
ready, so routine polls only check drive status instead of reading every track
again during audio extraction. Removal, disconnection, or a not-ready state
invalidates the cache.

Drive polling waits after each completed probe rather than catching up on
missed ticks. Slow probes increase that wait to twice their duration, capped
at five seconds (or the configured poll interval if longer), to give audio
reads time on the shared drive. Physical removal detection can consequently
take the waiting interval plus the next probe's duration. Fast probes retain
the normal polling interval; the web eject request can bypass the timer wait.

The Waveshare Rev2.1 brightness button is optional (see display setup below);
physical GPIO playback buttons remain a later milestone. Audio caching is temporary,
not a permanent ripped music library.

## Browser interface

The app also serves a small web page on port **8080**, with no login. From a
device on the same network, open `http://raspberrypi.local:8080` or
`http://<Pi-IP-address>:8080` (find the address with `hostname -I` on the Pi).

For your system MPD instance:

```sh
go run ./cmd/cdplayer -mpd 127.0.0.1:6600 -mpd-auto-device
```

The page shows disc presence, audio track numbers, playback state, elapsed/total
track time, and the latest player error. Click a track to play it, or use Play,
Pause, Stop, Previous, Next, and Eject. Eject stops and clears playback before
opening the configured drive's tray. If stopping fails, the tray is left alone;
if ejecting fails (for example, a locked tray), the page shows the error.
The button uses the installed `eject` utility, which supports unlocking and
alternative eject methods for USB drives, with a five-second command timeout.
Install it with `sudo apt install eject` if missing. Failures are also logged
in `journalctl -u cdplayer`. If terminal eject works but the service reports
permission denied, compare with `sudo -u cdplayer -g cdrom eject -v /dev/sr0`;
the app runs with that service account's permissions rather than your login's.
For intermittent boot or USB reconnection failures, collect diagnostics while
the error is present:

```sh
sudo bash scripts/diagnose-cd.sh
```

This reports the installed service settings, its actual running groups, optical
device permissions, and recent kernel/player logs without opening the drive or
changing settings. A brief permission error followed by `drive not ready`
does not establish a permanent group problem; check for device resets as well.

Album art, album/artist names, and track titles are looked up in the background.

The **Raspberry Pi** tab shows CPU temperature, overall CPU usage, used/total
memory, uptime, hostname, model, architecture, and core count. These measurements
refresh every two seconds independently of the CD player loop. CPU usage needs
two samples; unavailable sensors show “Unavailable”. CPU usage is calculated
from Linux CPU counters across all cores, with idle and I/O wait excluded from
busy time. Used memory is total minus available memory. No additional packages
or root access are needed for these measurements. The app user needs permission
to access the CD drive for ejecting (the provided service has the `cdrom` group).

Status refreshes once per second from memory; browser requests never scan the
disc. If the player loop stalls, the page marks the status as delayed and
control requests time out instead of waiting indefinitely.

Rapid Next/Previous clicks are combined: the page previews the selected track,
waits 250 ms after the last click, then sends one track selection. For example,
three Next clicks on track 1 jump directly to track 4. Other playback controls
cancel a pending selection; track changes stop at the first/last audio track.

## Start automatically with your existing system MPD

For a configured Pi, the update script performs the pull, build, installation,
and boot setup in one command. From your checkout, run as your normal user:

```sh
bash scripts/update.sh
```

It pulls the current branch's upstream using `git pull --ff-only`, tests and
builds the code, installs the compiled binary and the system-MPD service variant,
enables both services at boot, and restarts the app. MPD keeps your existing
`/etc/mpd.conf` and audio output settings. This uses port 6600, `/dev/sr0`, and
the single-drive `-mpd-auto-device` workaround. The script requests sudo only
for installation. Stop a manually running `go run` with Ctrl+C first.

The script requires a clean checkout and does not discard local changes. Pull
or build failures leave the installed version running. It saves the previous
binary as `/usr/local/bin/cdplayer.previous` before replacing it. If the new
service fails, inspect `journalctl -u cdplayer -n 50 --no-pager`; the script
reports failure rather than claiming the update succeeded.

Run the same command whenever you want to update. **Boot starts the installed
app; it does not run git pull or rebuild**, so playback at boot does not depend
on internet access. Git, Go, sudo, flock (util-linux), and a working system MPD
installation must already be available. The script itself must have reached
your Pi's checkout before you can run it the first time.

If playback already works with `-mpd 127.0.0.1:6600 -mpd-auto-device`, use the
service variant below. It keeps using your working `/etc/mpd.conf`, including
the ALSA output you configured. Run these commands **on the Pi**, from the
updated project directory. Stop any foreground `go run` with Ctrl+C first.

```sh
go build -o bin/cdplayer ./cmd/cdplayer
id cdplayer >/dev/null 2>&1 || sudo useradd --system --user-group --no-create-home --shell /usr/sbin/nologin cdplayer
sudo usermod -aG cdrom mpd
sudo install -m 0755 bin/cdplayer /usr/local/bin/cdplayer
sudo install -m 0644 deploy/cdplayer-system-mpd.service /etc/systemd/system/cdplayer.service
sudo systemctl daemon-reload
sudo systemctl enable mpd.service cdplayer.service
sudo systemctl restart mpd.service cdplayer.service
```

This launches the compiled app on every boot without a login, starts MPD first,
and gives the Go app drive permissions and a writable album cache. You do not
need to run `go run` again. The web interface remains on port 8080. Check it with:

```sh
systemctl status cdplayer --no-pager
journalctl -u cdplayer -f
```

To install future code updates, rebuild, reinstall the binary with the same
`sudo install` command, then run `sudo systemctl restart cdplayer`. Use this
variant or the dedicated MPD setup below; only one Go controller should run.
The auto-device workaround assumes one connected CD drive.

Use `-http :8090` to change the port or `-http ""` to disable the web interface.
The default `:8080` listens on all network interfaces. The existing systemd
service also enables the web page once you install the updated binary and
restart it. HTML, CSS, and JavaScript are embedded in the binary with no external
assets or JavaScript dependencies.

## Album information and artwork

On insertion, the app calculates a MusicBrainz disc ID from the existing table
of contents and looks up the matching release and track credits. It then fetches
the front cover from Cover Art Archive. Playback never waits for either request,
and no additional disc scans are needed. Track-specific artists are shown for
compilations; release artist credits are used as a fallback.

First-time lookups require internet access from the Pi. Successful metadata and
cover images are cached locally and served by the Pi, so later insertions can
show both offline. The default cache is `$XDG_CACHE_HOME/cdplayer`, or
`~/.cache/cdplayer`. Override it with `-cache-dir /path/to/cache`; an empty value
disables disk caching. The updated systemd unit uses `/var/cache/cdplayer` via
`CacheDirectory`. When upgrading an existing installation, reinstall the updated
`deploy/cdplayer.service`, run `sudo systemctl daemon-reload`, and restart the
service so this directory is writable under its filesystem restrictions.

Unknown discs keep numbered tracks and placeholder art. A failed lookup never
stops music. Failed album requests and temporary artwork failures retry after
15, 30, 60, then 120 seconds between attempts, without reinserting the disc.
The UI reports the failure reason; removing the disc cancels its lookup.
An unmatched disc or missing cover (HTTP 404) is not repeatedly requested. Missing
artwork does not hide available titles. If several editions match, the app uses
the first matching edition and displays a notice; manual edition selection is
not included. Metadata lookup supports standard all-audio CDs and CD Extra
discs with a verified final data-session boundary. CD Extra IDs exclude the
data track and session gap, as required by MusicBrainz. Other mixed layouts
still play but use numbered tracks. Ejecting or swapping discs
cancels pending requests and prevents old album information appearing on the
next disc.

The worker identifies itself to MusicBrainz and spaces requests at least 1.1
seconds apart. It fetches a bounded cover thumbnail rather than a full-size scan.
Sources: [MusicBrainz disc IDs](https://musicbrainz.org/doc/Disc_ID_Calculation),
[MusicBrainz request policy](https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting),
[Cover Art Archive](https://musicbrainz.org/doc/Cover_Art_Archive/API).

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
sudo apt install mpd mpc eject alsa-utils python3 python3-mutagen cd-paranoia
mpd --version
ls -l /dev/sr*
aplay -l
```

In `mpd --version`, confirm HTTP input and WAV decoding support for cached
playback. The `cd-paranoia` package supplies the libcdio CDDA/paranoia libraries;
the app's embedded Python helper loads them using the standard-library `ctypes`
module (no pip dependencies or C compiler needed). `-device auto` selects the
single optical drive. The older `-audio-cache=false` path requires MPD's
**cdio_paranoia** input plugin instead.

The app defaults to the system MPD on **127.0.0.1:6600**, matching
`scripts/update.sh`. The optional dedicated setup below uses its own MPD
instance on **127.0.0.1:6601**; pass `-mpd 127.0.0.1:6601` when running
the app manually with that setup. It owns that instance's queue and playback
settings. An existing MPD on port 6600 can remain
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
no external Go dependencies; Python 3, the libcdio runtime libraries, and MPD
are needed on the Pi. The reader helper is embedded in the Go executable.

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
node --test internal/web/navigation.test.cjs
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

## Spotify Connect (optional)

The app can run an ALSA-enabled **librespot** receiver. On your phone, select
**Raspberry Pi CD Player** in Spotify's device picker on the same LAN. Spotify
Premium is required by [librespot](https://github.com/librespot-org/librespot).
Connecting a Spotify session immediately requests that CD playback stop.
The [blocking sink hook](https://github.com/librespot-org/librespot/wiki/Events)
stops and clears MPD before Spotify opens the sound output. If MPD cannot be
stopped, Spotify waits instead of playing over the CD.

While Spotify is selected, inserting a CD does not start it. Pausing Spotify or
moving playback to your phone leaves the CD stopped. **Switch to CD** in the web
UI disconnects the receiver and starts the current disc from track 1. The
receiver then advertises again for your next Spotify session. CD controls are
disabled during Spotify; eject remains available. Spotify tracks, volume, and
play/pause are controlled from your phone; the web UI shows the selected source
and receiver availability, plus Spotify album art, title, artist, album, and playback status. Disc information is the
last observation while Spotify is selected, since drive polling is suspended.

On the Pi:

1. Install an ALSA-enabled `librespot` binary. The
   [Raspotify installation instructions](https://github.com/dtcooper/raspotify)
   provide packaged binaries. Check OS compatibility: current Raspotify packages
   target Debian 13 / Trixie; do not force these packages onto an older OS.
2. Once these repository changes are available on your Pi, run:

   ```sh
   bash scripts/update.sh
   bash scripts/setup-spotify.sh
   ```

The setup script requires an installed, updated `cdplayer.service`. It disables
Raspotify's standalone service, because the Go app manages the receiver itself,
and writes `/etc/cdplayer/spotify.conf` if it does not already exist. Both CD
playback and the receiver start through `cdplayer.service` at boot; repository
updates preserve the Spotify configuration. The initial source after boot is CD.

The default Spotify output is the Pi headphone jack:

```ini
CDPLAYER_SPOTIFY_ENABLED=1
CDPLAYER_SPOTIFY_BINARY="/usr/bin/librespot"
CDPLAYER_SPOTIFY_NAME="Raspberry Pi CD Player"
CDPLAYER_SPOTIFY_DEVICE="plughw:CARD=Headphones,DEV=0"
```

Use the **same ALSA output** as MPD. Change the device/name in that file if needed,
then `sudo systemctl restart cdplayer`. The service has the `audio` group to open
the output. To disable integration, set `CDPLAYER_SPOTIFY_ENABLED=0` and restart.
No Spotify password or developer API key is needed; credentials are not cached.

For foreground development, stop `cdplayer.service` and any standalone Raspotify
service first, then run:

```sh
go run ./cmd/cdplayer -mpd 127.0.0.1:6600 -mpd-auto-device -spotify \
  -spotify-device 'plughw:CARD=Headphones,DEV=0'
```

Keep the web interface enabled for the local handoff callback. The receiver
binary and app executable must be accessible to the service account. Check
`journalctl -u cdplayer -f` for receiver/audio errors. A crashed receiver retries
after five seconds; it does not automatically resume the CD. USB drive power
faults still need resolving separately.

Hardware check after installation: play a CD, transfer Spotify to the Pi, check
that the CD stops, pause Spotify and insert a disc (it should stay silent), then
choose Switch to CD. Automated tests cover source transitions, failed handoffs,
receiver restarts, and stale callback rejection; real Spotify playback requires
verification on the Pi with your account and audio output.

### Automatic USB CD drive selection

`-device auto` is now the default, including both boot service templates. The
app checks Linux `/sys/class/block/*/device/type` for the single CD/DVD device
and uses its `/dev` node. Audio CDs do **not** need to be mounted. It waits when
no drive is connected and follows changes such as `/dev/sr0` to `/dev/sr1` after
reconnection, discarding the old queue and TOC when the selected path changes.
Automatic mode also lets MPD discover the drive, avoiding its explicit CD-device
URL parsing problem on affected versions. Only connect one optical drive; if
multiple are found, the app asks for an explicit `-device /dev/srN` instead.

```sh
go run ./cmd/cdplayer -mpd 127.0.0.1:6600 -device auto
```

After syncing these changes, `bash scripts/update.sh` installs the updated boot
service. An explicit `-device /dev/sr0` still pins the app to that path. Automatic
selection addresses device renaming, not USB power loss or a hung drive: kernel
`over-current` and repeated USB disconnect messages still require checking the
power supply, cable, or powered USB hub. A disconnect/reconnect entirely between
two polls at the same device path may not be observed.

### Spotify connects but skips tracks without sound

Connection alone does not confirm successful audio playback. Reproduce the
problem and collect the receiver's errors before changing the sound device:

```sh
sudo bash scripts/diagnose-spotify.sh
```

This read-only script collects the receiver version, service states, sound
hardware and its current owners, output selection, and recent logs. It does not
stop music, restart services, or read Spotify credentials. If you run the app
in a terminal instead of the boot service, also capture that terminal's errors.

ALSA `Device or resource busy`, permission, or unsupported-format errors point
to audio setup; stream download, authentication, or unavailable-track errors
require a different fix. Do not assume every skipping problem is the CD handoff.
Only the app-managed receiver should be active; a standalone `raspotify.service`
may represent a different Spotify device and will not run the app's handoff.


If receiver logs report `ReadOnlyFilesystem` for `/tmp/.tmp...`, update the
service: librespot requires writable temporary storage even with audio caching
disabled. Both service templates set `PrivateTmp=true`, which provides writable
private `/tmp` and `/var/tmp` alongside `ProtectSystem=strict`. The normal updater
installs this fix. To fix an existing Pi installation immediately:

```sh
sudo mkdir -p /etc/systemd/system/cdplayer.service.d
printf '[Service]\nPrivateTmp=true\n' | sudo tee /etc/systemd/system/cdplayer.service.d/spotify-tmp.conf
sudo systemctl daemon-reload
sudo systemctl restart cdplayer
```

Reconnect Spotify after restarting. If the rapid skipping also produced
`429 Too Many Requests`, pause playback and allow the rate limit to clear before
trying again. The logs do not specify how long that will take.

After an MPD command timeout, the app reconnects and checks the queued track
URIs against the unchanged disc. If the queue still matches, it preserves MPD's
current track and play/pause/stop state instead of restarting the album. A failed
queue check is retried without rebuilding the queue; a missing or changed queue
is rebuilt. This prevents a slow track change from triggering an unnecessary
album restart, but does not make a stalled CD drive respond faster.


After Spotify disconnects, the web UI shows **Play CD** and a stopped state.
Disconnecting, inserting another disc, or reconnecting MPD does not start the CD
in this state. Press Play CD to start the current disc from track 1. Pausing
Spotify keeps Spotify selected; Switch to CD remains available for manual return.
Spotify track metadata comes from librespot events, with HTTPS artwork loaded by
your browser. Missing artwork falls back to the placeholder. Track and artwork
information clears on disconnect and changes with each track.

## Small TFT display preview

`http://<pi-address>:8080/display` provides a compact landscape 320×240 touch
view: album art, song title, artist, album, source, and four CD controls
(Previous, Pause, Play, Next). It uses the same playback controller and navigation
debouncing as the main web UI. Spotify artwork and track information appear
while Spotify is selected; Spotify playback controls remain on the phone.
After Spotify disconnects, Play becomes available without automatically starting
the CD. The full web interface remains at `/`.

This is a browser view, not a TFT driver. Display output, touch calibration,
physical GPIO key mapping depend on the exact HAT model and installed Raspberry
Pi OS/display stack. For the configured Waveshare/X11 setup, kiosk startup and
a branded boot splash can be installed as described below.
Preview with a 320×240 browser viewport. Keyboard equivalents for testing are
Left/Right arrows (previous/next), P (pause), and Enter (play); these do not read
GPIO buttons. Do not guess GPIO pins from the screen size alone.

### Physical brightness button (Waveshare Rev2.1)

On the **2.8inch RPi LCD (A) marked Rev2.1**, **KEY4** can cycle brightness:
**20% → 40% → 60% → 80% → 100% → 20%**. The first start uses 60%; subsequent
starts restore the last selected level. Holding a button does not repeat, and
switch bounce is filtered. The lowest setting stays visible rather than turning
the panel off. Percentages are PWM duty levels, not measured luminance.

Check **Rev2.1 on the back of the LCD** before installing. Waveshare documents
PWM brightness only on that revision; older boards use different wiring.
On your Pi 4, from the updated checkout, run as the normal user:

```sh
bash scripts/setup-brightness-button.sh --rev2.1
```

No reboot is needed. The separate `cdplayer-backlight.service` starts at boot and
works independently of the browser, player and USB scan. It saves the selected
level to `/var/lib/cdplayer-backlight/brightness`. The display/splash installers
do not enable this hardware-specific feature automatically.

For Rev2.1, KEY4 connects to **BCM GPIO25 (header pin 22)**, and the backlight
control connects to **BCM GPIO18 (header pin 12)**. The other three buttons are
left available. The service uses GPIO Zero with the lgpio backend, software PWM
at 1 kHz, and an input pull-up. It does not change the display overlay, X11
rotation, touch calibration, or hardware PWM clock configuration. If a kernel
driver or another program owns a required GPIO line, the service reports an
error instead of taking over that line. Actual dimming and button operation
must be checked on the physical Rev2.1 panel, including during music playback.

```sh
systemctl status cdplayer-backlight --no-pager
journalctl -u cdplayer-backlight -n 30 --no-pager
```

To disable automatic brightness control:

```sh
sudo systemctl disable --now cdplayer-backlight
```

Sources: [Waveshare PWM instructions](https://www.waveshare.com/wiki/2.8inch_RPi_LCD_%28A%29#PWM_Backlight_Adjustment_Function),
[Waveshare Rev2.1 pin table](https://www.waveshare.net/wiki/2.8inch_RPi_LCD_%28A%29),
and [GPIO Zero's lgpio backend](https://gpiozero.readthedocs.io/en/stable/_modules/gpiozero/pins/lgpio.html).

### Set up automatic login to the player on another Pi

For another **64-bit Raspberry Pi 4 with the same Waveshare LCD V2**, first
install its LCD driver and the CD player service (see the MPD/service setup
above). The LCD must appear as `/dev/fb0` with `fb_st7789v` in `/proc/fb`.
Go and Python 3 must be installed, and the checkout must include the root PNG.
The Pi can still boot to the console; a working desktop or existing auto-login
is not required.

From the project folder, run as the normal user who should log in automatically:

```sh
bash scripts/setup-display.sh --rotate right
sudo reboot
```

This installs Chromium, LightDM, Xorg, the framebuffer/input drivers and Openbox;
creates the SPI Xorg configuration if missing; enables graphical boot; and sets
LightDM to automatically log in **the user running the script**. It builds and
installs the updated player using the existing service, then opens the Comreact
loading page followed by `http://localhost:8080/display` in fullscreen Chromium.
The browser uses the dedicated profile with the keyring prompt bypassed. All
paths work for the chosen username; nothing requires a user named `gillzon`.

Omit `--rotate` to preserve existing rotation (or use the driver's default on a
new configuration). Options are `normal`, `right`, `left`, and `inverted`.
Existing touch calibration is preserved; a changed orientation may require
recalibration. The script does not download/install the hardware overlay or
configure MPD/audio, Wi-Fi, or touchscreen calibration.

For **auto-login plus the early boot logo in one command**, use this instead:

```sh
bash scripts/setup-boot-splash.sh --rotate right
sudo reboot
```

Both installers back up replaced configuration and the previous boot target.
They do not reboot or restart the desktop automatically. The splash installer
also requires the automatic initramfs described below.

### Comreact boot logo and automatic display startup

For the **Raspberry Pi 4 with a working Waveshare 2.8inch RPi LCD (A) V2**, the
installer uses `comreact-logo-WHITE.png` from the repository root as a white logo
on black. It installs a Plymouth boot theme and a dedicated X11 kiosk session.
The same logo remains visible while Chromium starts and the player loads; the
local startup page reveals `/display` only after it renders its first successful
`/api/status` response. An unavailable app keeps the logo on screen and retries.

Prerequisites: 64-bit Raspberry Pi OS Trixie/Bookworm with the automatic boot image
at `/boot/firmware/initramfs8`, Go, Python 3, the existing
`cdplayer.service`, and the SPI LCD registered as `/dev/fb0` (`fb_st7789v`).
The installer creates `/etc/X11/xorg.conf.d/98-spi-screen.conf` if absent and
configures desktop auto-login, including on a Pi currently booting to console.
An existing Xorg file must select `fbdev` and `/dev/fb0`. Its settings and touch
calibration are preserved unless you explicitly select `--rotate`; the early
splash uses the resulting Xorg rotation.

Copy/sync the updated checkout **including the root PNG** to the Pi. From that
checkout, run as the desktop auto-login user (`gillzon`), **without sudo**:

```sh
bash scripts/setup-boot-splash.sh
sudo reboot
```

The script refuses to run on a workstation. It checks the target, tests and
builds the app, requests sudo, installs the required packages, and installs the
new binary without changing your CD/MPD/Spotify service flags. It adds the SPI
display modules to initramfs, enables the Plymouth splash in the existing kernel
command line, rebuilds initramfs, and checks that the firmware's `initramfs8`
contains the theme. Boot options such as `root=`, console
devices, and display overlays are preserved. The firmware rainbow is disabled.

LightDM auto-login changes to **CD Player Kiosk**, using Openbox without LXDE's
panel/file-manager desktop. The existing personal autostart files remain in place
but are not used by this session. The launcher uses the existing dedicated
`~/.config/chromium-cdplayer` profile and `--password-store=basic`; do not save
passwords in this profile. Chromium restarts if it exits. Its local startup page
loads without the app or internet, then displays the real player in a full-screen
frame. Direct access to `/display` on other devices continues to work normally.

The logo cannot appear on this SPI LCD until Linux initializes its driver.
Plymouth and Xorg also need to transfer ownership of the screen, so a brief black
transition may still occur; this is not a replacement for the Pi's firmware.
The early theme, framebuffer rotation and physical boot transition must be
verified on the Pi. After installation, reboot and check both the logo's
orientation and the controls. If you change Xorg rotation later, rerun the setup
script so Plymouth uses the same orientation.

Diagnostics over SSH:

```sh
cat ~/.local/state/cdplayer-kiosk.log
systemctl status lightdm cdplayer --no-pager -l
curl -fsS http://localhost:8080/display | grep 'cdplayer:display-ready'
plymouth-set-default-theme
```

If the logo stays visible, inspect `journalctl -u cdplayer -b --no-pager` and
confirm the updated app is running. The startup wrapper intentionally does not
mistake an HTTP error page or a mere browser window for a ready display.

Both display-only and full splash installations print a dated backup under
`/var/lib/cdplayer-boot-backups/`, covering the replaced files and binary. To
restore the setup from immediately before that installation, use the exact
printed backup directory:

```sh
sudo python3 scripts/restore-boot-splash.py /var/lib/cdplayer-boot-backups/PRINTED-DIRECTORY
sudo reboot
```

Restoration leaves installed packages available and restores the backed-up
configuration and previous default boot target, so later manual edits to those
files would also be replaced.
Normal `scripts/update.sh` app updates preserve the installed boot configuration;
rerun `setup-boot-splash.sh` when changing the logo or boot assets.

Boot configuration and browser handoff checks (no system changes):

```sh
python3 -m unittest discover -s scripts -p 'test_boot_splash.py'
node --test internal/web/boot.test.cjs
```

References: [Plymouth theme installation](https://manpages.debian.org/trixie/plymouth/plymouth-set-default-theme.1.en.html),
[LightDM session configuration](https://github.com/canonical/lightdm/blob/main/data/lightdm.conf),
and [Waveshare's LCD instructions](https://www.waveshare.com/wiki/2.8inch_RPi_LCD_%28A%29).


Track selections are accepted into a bounded queue immediately; pending status
and any later MPD error are reported through the status API. The selected track updates on that
acknowledgment, and subsequent status polling reports playback errors separately.
This reduces unnecessary button blocking, but drive seeks or an MPD command
that itself takes too long can still delay a track change.

Playback commands (`play`, `next`, and `previous`) allow up to 15 seconds for
MPD to acknowledge optical-drive seeks. Routine commands retain a five-second
deadline. The web control request allows 25 seconds including time waiting for
the controller, with a 30-second browser deadline and 35-second server write
deadline. A timeout still means the command's outcome is unknown, not that MPD
cancelled it. The UI shows a waiting indication while a command is pending.
This accommodates slow acknowledgements; it does not speed up disc reading.


## Persistent CD reader and background audio cache

Cached playback is enabled by default. Update the Pi with:

```sh
sudo apt install python3 cd-paranoia
bash scripts/update.sh
```

The updater runs `cdplayer -check-audio` before replacing the installed binary.
This checks the runtime libraries without opening the drive. For a foreground
run: `go run ./cmd/cdplayer -mpd 127.0.0.1:6600` (stop the boot service first).

To experiment with a lower reading speed, add `-cd-speed 4` for a 4× request.
Without a flag, `-cd-speed 0` leaves speed selection to the drive. The supplied
boot services use this default; `bash scripts/update.sh` removes the earlier
4× restriction for subsequent starts and reboots. After using a speed cap,
power-cycle the optical drive once to clear any speed setting retained by its
firmware. This option
applies to cached audio only, after libcdio opens and initializes the drive.
The helper logs whether the request was accepted or rejected; acceptance does
not measure actual speed or current. It cannot control the initial spin-up
acceleration or guarantee operation within the Pi's USB power budget. A lower
speed can also slow cache filling. A foreground test does not change the service.

- One helper owns the digital audio-reading session and seeks within that
  session instead of reopening the drive for each MPD track URL. By default,
  audio is read directly with libcdio's CDDA API, bypassing the paranoia
  reader's buffering and software verification/repair.
  Use `-audio-verify` to restore libcdio-paranoia's full correction mode for
  difficult discs. Without it, damaged discs or inaccurate reads can produce
  audible errors. Hardware spin-up and drive initialization still take time;
  the app starts after the first audio block, not after caching a whole track.
  That first block wakes the player immediately instead of waiting for its
  next polling tick. Logs report reader-open time and first-block read time
  separately. The “determine drive endianness” message is the audio library
  checking sample byte order, not Linux detecting speed or mounting the CD.
  Album artwork lookup runs separately and does not delay playback.
- Audio is cached in one-second blocks and playback starts before the whole
  track is cached. The reader stays on the selected track until it is fully
  cached, then reads following tracks. It does not seek away to prepare nearby
  track intros while the selected track is incomplete. Manual track selection
  takes priority after the current physical read finishes; it cannot interrupt
  a kernel drive read halfway through. Uncached track changes still need a seek.
  A drive that reads slower than playback can still exhaust the audio buffer.
- A separate **loopback-only**, dynamically allocated HTTP port serves WAV data
  to MPD. It supports byte ranges and waits for missing blocks. MPD and cdplayer
  must run on the same machine. This audio endpoint works even with `-http ''`.
- The browser accepts track selections without waiting for MPD or CD I/O, shows
  the requested track, and retains only the latest waiting selection. An already
  executing MPD command may finish before the newer selection is applied. Pause,
  Stop, eject, and source changes cancel waiting track selections.
- A background monitor owns drive-status probes, so slow probe ioctls do not
  block web controls. The normal disc-ID guard still rejects stale selections.
- The cache lives under `<cache-dir>/audio`, normally
  `/var/cache/cdplayer/audio` for the service. Budget about **850 MB for an
  80-minute disc**, plus filesystem overhead. Use `-cache-dir` to choose storage;
  with an empty cache directory, audio uses the system temporary directory.
  The process locks its audio-cache directory to prevent concurrent use.
- Removal, eject, Spotify takeover, and shutdown cancel audio streams and remove
  the session cache. Obsolete session directories from a crash are cleaned on
  the next start. A fully cached disc releases its reader but retains audio until
  the session ends. Reinsertion or returning from Spotify starts a fresh cache.
- The web UI shows the audio cache percentage and reader errors. A read failure
  is surfaced instead of returning unfilled file data as silence. Correct the
  underlying error and reinsert the disc to retry. A stalled helper operation has
  a 30-second deadline; drive monitoring stays independently responsive.

Cached track changes avoid optical reads. First insertion and uncached seeks
still depend on spin-up, disc condition, and drive speed; no fixed playback
latency is promised. The test suite verifies real libcdio reads and seeks against
a generated BIN/CUE audio disc image when the runtime libraries are installed,
as well as streaming, cancellation, and queue prioritization with simulated
readers. Raspberry Pi optical-drive/audio testing is still required.

To compare with the old path or use a remote MPD:

```sh
go run ./cmd/cdplayer -mpd 127.0.0.1:6600 -audio-cache=false -mpd-auto-device
```

### Sound output settings

Open **Settings → Sound output** in the web UI to choose HDMI, 3.5 mm headphones,
USB audio, or another configured MPD output. Press **Apply output** to select
one output. The app saves its name in `audio-output` under `-cache-dir` and
restores it when connecting to MPD after a restart; numeric card/output IDs
are not used as the saved preference.

`scripts/update.sh` adds the Pi's detected ALSA playback devices to `/etc/mpd.conf`
and restarts MPD. Existing configuration is preserved, with a backup at
`/etc/mpd.conf.before-cdplayer-outputs`. The new outputs initially stay disabled
until selected. For a manual installation, or after adding a USB sound card:

```bash
sudo python3 scripts/configure-outputs.py /etc/mpd.conf
sudo systemctl restart mpd
```

For the dedicated MPD service, pass the configuration file used by that service
instead, then restart `cdplayer-mpd`. Refresh outputs in Settings afterwards.
HDMI outputs must be exposed by Linux and connected to an audio-capable display
or receiver to produce sound; the selector cannot enable missing hardware.

Outputs labelled **CD + Spotify** apply to both sources. Selecting an output
restarts the Spotify receiver and disconnects the phone; reconnect in Spotify
when ready. It does not automatically start the CD after Spotify disconnects.
Existing custom outputs are labelled **CD only** and leave Spotify's destination
unchanged. A brief interruption during switching is normal. No root access is
used by web requests: switching uses MPD's output protocol and the app's own
Spotify process. MPD configuration is only changed by the installation script.


## USB MP3 library

The player can index a mounted USB drive into SQLite on the Pi. Open **Pick song ·
USB Music**, then type an artist, album, song title, or a combination in the single
search field. Search ignores case and accents; every word must match somewhere
in the title, artist, or album. Results show 20 songs per page, with Previous/Next page buttons and a page indicator. Select a song to
start it and queue the other songs from the same album and folder in track order.
Previous/Next move within that queue. **Mix all** shuffles every available song in
the USB library into a new queue, regardless of the current search or page. Each
song appears once; the mix stops at the end. The **Shuffle** button on `/display`
starts the same USB mix, including when CD or Spotify is selected. Playback uses
your selected MPD output.

MP3 tags supply the title, artist, album, track number, and duration. Missing tags
fall back to the filename, “Unknown artist”, and folder name. Embedded JPEG/PNG
artwork takes priority over `cover.jpg`, `cover.jpeg`, `cover.png`, `folder.jpg`,
`folder.jpeg`, or `folder.png` (case insensitive). Artwork is deduplicated in the
database and limited to 5 MiB per image. The main UI and `/display` show the
currently playing USB song and artwork. The display also shows a local clock,
elapsed/total song time, and a progress bar for USB, CD, and Spotify playback.
The display stays within 320×240 pixels, even in a larger browser window. Long
titles wrap to two lines and then truncate; artist and album names use ellipses. Metadata is read with
[Mutagen](https://mutagen.readthedocs.io/en/latest/api/mp3.html); SQLite uses
Python's standard library, so the Go binary still builds without CGO.

### Set up the Pi

Install the metadata reader before updating/installing the new player:

```sh
sudo apt install python3-mutagen
```

The default music location is `/media/cdplayer`. Mount your USB partition there
and ensure the `cdplayer` service user can list folders and read the music files.
For an initial check, identify the USB partition with `lsblk -f`, then mount it
using its filesystem UUID (replace `YOUR_USB_UUID`):

```sh
sudo mkdir -p /media/cdplayer
sudo mount -o ro /dev/disk/by-uuid/YOUR_USB_UUID /media/cdplayer
sudo -u cdplayer ls /media/cdplayer
```

Configure a persistent mount through your Pi's `/etc/fstab` if it should survive
reboot; use the filesystem UUID and `nofail` so an absent drive does not prevent
boot. The application scans mounted files; it does not mount or format drives.
For FAT/exFAT, mount permissions must allow the service user to read the files;
for Linux filesystems, the directory and file permissions must allow it. The MPD
user does not need access to the drive: the app streams selected MP3s to local
MPD over a separate loopback HTTP server with range support.

To use another location, create `/etc/cdplayer/music.conf` with:

```ini
CDPLAYER_MUSIC_DIR=/your/mounted/music
```

Both supplied systemd units read this optional file. Restart `cdplayer` after
changing it. Existing Spotify configuration stays in its own file. For a manual
run, use `-music-dir=/your/mounted/music`; `-music-dir=` disables the USB library.
Choose a mount outside `/home`, because the supplied service protects home
directories. USB playback requires MPD on this Pi and a writable `-cache-dir`.

At startup the player loads the saved SQLite library on the Pi, without walking
or checking the USB disk. A completed scan is reused across reboots, including
an empty library; existing indexes from older versions are reused too. There is
no periodic full-disk scan. **Refresh library** scans for added, changed or removed
files whenever you choose. The first scan starts automatically only if there are
no saved songs and no record of a completed scan. If that attempt fails (for
example, the disk is not mounted yet), mount the disk and press Refresh library.
Large scans have no ten-minute cutoff; they run until complete or the service stops.
The index lives at `<cache-dir>/library.sqlite` (normally
`/var/cache/cdplayer/library.sqlite` under the supplied services). Scans update
changed files, remove missing entries after a successful scan, and skip symlinks.
The USB filesystem is never modified. Scans save metadata and artwork in batches
of **100 files or five seconds**, whichever comes first (checked after each file).
SQLite uses WAL with `synchronous=FULL` so completed commits are synced to storage.
Songs from saved batches are searchable and playable before the whole scan ends.

After a power loss or restart during a scan, any saved songs are usable immediately.
The browser shows **Scan paused**; press **Refresh library** to continue when
convenient. Resuming recounts the file paths and compares file timestamps, sizes
and folder artwork with the saved index. Unchanged saved songs skip MP3 tag
parsing. It resumes the saved indexing work rather than
re-reading every song; it does **not** persist the directory-walk position, so
counting and checking the file list repeats. Only the unfinished batch needs to
be redone. As with any filesystem, durability depends on the storage device
honoring flushes; this does not protect against a damaged SD card or USB disk.

Under **Pick song · USB Music**, progress first shows **Counting MP3 files…** and
the number found, with an indeterminate bar. Once the count is known it shows a
percentage, checked/total files and saved songs. The percentage measures files
checked, not bytes or estimated time remaining; it reaches 100% only after final
cleanup succeeds. A temporary SQLite path list avoids keeping the entire file
list in Python memory. Search filters do not change the scan's total.
When not scanning, **Using saved library** confirms that the index was loaded
from disk. A filtered search shows “matching songs” to distinguish its results
from the total saved library. Keep the same music mount path and persistent cache
directory across reboots. The USB disk must be mounted to play songs; browsing
the saved list alone does not establish that the disk is connected.

An unavailable directory or failed scan shows an error, retains existing songs
and completed batches, and does not prune missing entries. An empty mounted
directory clears its entries after a successful scan. A removed song cannot be
played even if an old search result remains visible. Scanning and searching run
separately from the playback loop.

Selecting USB music disconnects Spotify and suspends CD autoplay. Inserting a CD,
stopping USB playback, or unplugging the drive does not start the CD. Choose
**Switch to CD** to return, or reconnect Spotify to take over playback. Eject
opens the CD tray without stopping USB music. After an MPD restart that loses its
queue, pick a USB song again. As before, restarting the application starts in CD
mode.

Library checks (Python tests require `python3-mutagen`):

```sh
go test ./...
python3 -m unittest discover -s internal/library -p '*_test.py'
node --test internal/web/*.test.cjs
```

An optional integration smoke test generates MP3 fixtures with `ffmpeg` and runs
the built application against a simulated MPD on localhost:

```sh
go build -o /tmp/cdplayer-usb ./cmd/cdplayer
python3 scripts/test-usb.py /tmp/cdplayer-usb
```

On the Pi, also check real MP3 playback, pause/resume and album navigation, then
unplug/reinsert the USB drive and refresh. Try switching among USB, CD, and Spotify
with a disc inserted to confirm the selected output and source handoffs.
