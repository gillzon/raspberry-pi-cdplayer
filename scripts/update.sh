#!/usr/bin/env bash
# Run as your normal user on the Pi: bash scripts/update.sh
set -Eeuo pipefail

# Parse the whole function before pulling, since git may replace this script.
main() {
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"

if [[ "$(uname -s)" != Linux ]]; then
    echo "Run this script on the Raspberry Pi running Linux." >&2
    exit 1
fi
if (( EUID == 0 )); then
    echo "Run as your normal user, without sudo. The install steps will request sudo." >&2
    exit 1
fi
for dependency in git go sudo systemctl flock getent eject python3; do
    if ! command -v "$dependency" >/dev/null 2>&1; then
        echo "Missing command: $dependency. Install it before running this script." >&2
        exit 1
    fi
done
if ! command -v mpd >/dev/null 2>&1 || ! id mpd >/dev/null 2>&1; then
    echo "Install and configure MPD first (sudo apt install mpd mpc). See README.md." >&2
    exit 1
fi

if ! python3 -c 'import sqlite3, mutagen' >/dev/null 2>&1; then
    echo "USB library dependencies missing. Run: sudo apt install python3-mutagen" >&2
    exit 1
fi

# Prevent two updates from installing different builds concurrently.
exec 9>"$(git rev-parse --git-path cdplayer-update.lock)"
if ! flock -n 9; then
    echo "Another update is already running." >&2
    exit 1
fi
if [[ -n "$(git status --porcelain)" ]]; then
    echo "The checkout has local changes. Commit/stash them before updating." >&2
    echo "No files have been discarded and the installed app is unchanged." >&2
    exit 1
fi
if ! git symbolic-ref --quiet HEAD >/dev/null || ! git rev-parse --verify '@{upstream}' >/dev/null 2>&1; then
    echo "Check out a branch with an upstream remote before updating." >&2
    exit 1
fi

echo "Pulling the latest version of the current branch…"
git pull --ff-only

build_dir="$(mktemp -d)"
trap 'rm -rf -- "$build_dir"' EXIT
echo "Building and testing $(git rev-parse --short HEAD)…"
go test ./...
python3 -m unittest discover -s internal/library -p '*_test.py'
go build -trimpath -o "$build_dir/cdplayer" ./cmd/cdplayer
"$build_dir/cdplayer" -check-audio
cp deploy/cdplayer-system-mpd.service "$build_dir/cdplayer.service"

# Fetch and compile as the checkout owner; privilege is only needed to install.
sudo -v
if ! id cdplayer >/dev/null 2>&1; then
    sudo useradd --system --user-group --no-create-home --shell /usr/sbin/nologin cdplayer
fi
if ! getent group cdrom >/dev/null; then
    echo "The cdrom group is missing; configure CD drive permissions first." >&2
    exit 1
fi
restart_mpd=false
# Add selectable outputs using stable ALSA card names. Existing audio outputs
# remain enabled until the user chooses a destination in Settings.
if [[ -d /proc/asound ]]; then
    sudo python3 scripts/configure-outputs.py /etc/mpd.conf
    restart_mpd=true
fi
if [[ " $(id -nG mpd) " != *" cdrom "* ]]; then
    sudo usermod -aG cdrom mpd
    restart_mpd=true
fi

echo "Installing the application and enabling startup at boot…"
# Keep one previous binary for recovery, then replace the executable atomically.
if sudo test -f /usr/local/bin/cdplayer; then
    sudo cp -p /usr/local/bin/cdplayer /usr/local/bin/cdplayer.previous
fi
sudo install -m 0755 "$build_dir/cdplayer" /usr/local/bin/cdplayer.new
sudo mv -f /usr/local/bin/cdplayer.new /usr/local/bin/cdplayer
sudo install -m 0644 "$build_dir/cdplayer.service" /etc/systemd/system/cdplayer.service
sudo systemctl daemon-reload
sudo systemctl enable mpd.service cdplayer.service
if "$restart_mpd"; then
    sudo systemctl restart mpd.service
else
    sudo systemctl start mpd.service
fi
sudo systemctl restart cdplayer.service
sleep 2
if ! systemctl is-active --quiet cdplayer.service; then
    echo "The new service did not stay running. Inspect: journalctl -u cdplayer -n 50" >&2
    echo "If a previous binary existed, it is saved at /usr/local/bin/cdplayer.previous." >&2
    exit 1
fi
echo "Updated to $(git rev-parse --short HEAD). CD player is running and enabled at boot."
echo "Web UI: http://raspberrypi.local:8080 (or use the Pi's IP address)."
}

main "$@"
