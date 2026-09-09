#!/usr/bin/env bash
set -Eeuo pipefail
if [[ ! -r /sys/class/backlight/rpi_backlight/max_brightness ]] || [[ "$(cat /sys/class/backlight/rpi_backlight/max_brightness)" != 1 ]]; then
    echo 'This setup requires the on/off rpi_backlight driver on your Pi.' >&2
    exit 1
fi
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
python3 - <<'PY'
import json, urllib.request
with urllib.request.urlopen('http://127.0.0.1:8080/api/status', timeout=5) as response:
    if 'screen_asleep' not in json.load(response):
        raise SystemExit('Update and restart cdplayer before installing screen sleep.')
PY
if [[ -f /etc/systemd/system/cdplayer-backlight.service ]]; then
    sudo systemctl disable --now cdplayer-backlight.service
fi
sudo install -D -m 0644 "$repo_dir/scripts/screen-sleep.py" /usr/local/lib/cdplayer/screen-sleep.py
sudo install -m 0644 "$repo_dir/deploy/cdplayer-screen.service" /etc/systemd/system/cdplayer-screen.service
sudo systemctl daemon-reload
if systemctl is-failed --quiet cdplayer-screen.service; then
    sudo systemctl reset-failed cdplayer-screen.service
fi
sudo systemctl enable cdplayer-screen.service
sudo systemctl restart cdplayer-screen.service
sleep 2
if ! systemctl is-active --quiet cdplayer-screen.service; then
    sudo journalctl -u cdplayer-screen.service -n 30 --no-pager
    exit 1
fi
echo 'Screen sleep ready. Tap Sleep on the display; a Spotify connection wakes it.'
