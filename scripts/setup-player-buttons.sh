#!/usr/bin/env bash
set -Eeuo pipefail
if [[ "${1:-}" == --help ]]; then
    echo 'Usage: bash scripts/setup-player-buttons.sh --rev2.1'
    echo 'KEY1 Mode, KEY2 Play/Pause, KEY3 Next, KEY4 Previous. Disables brightness buttons.'
    exit 0
fi
if [[ $# != 1 || "$1" != --rev2.1 ]]; then
    echo 'Verify the Rev2.1 marking on the LCD and pass --rev2.1.' >&2
    exit 1
fi
if (( EUID == 0 )); then
    echo 'Run as your normal Pi user, without sudo.' >&2
    exit 1
fi
if [[ ! -r /proc/device-tree/model ]] || ! grep -aq 'Raspberry Pi 4 Model B' /proc/device-tree/model; then
    echo 'Run this installer on the Raspberry Pi 4.' >&2
    exit 1
fi
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
# Check that the player was upgraded before replacing working hardware controls.
python3 - <<'PY'
import json, urllib.request
with urllib.request.urlopen('http://127.0.0.1:8080/api/radio', timeout=5) as response:
    assert isinstance(json.load(response), list), 'Update cdplayer first'
PY
sudo apt-get update
sudo apt-get install -y python3-gpiozero python3-lgpio
if [[ -f /etc/systemd/system/cdplayer-backlight.service ]]; then
    sudo systemctl disable --now cdplayer-backlight.service
fi
sudo install -D -m 0644 "$repo_dir/scripts/player-buttons.py" /usr/local/lib/cdplayer/player-buttons.py
sudo install -m 0644 "$repo_dir/deploy/cdplayer-buttons.service" /etc/systemd/system/cdplayer-buttons.service
sudo systemctl daemon-reload
# A newly installed unit may not be loaded yet. Only reset an existing failure.
if systemctl is-failed --quiet cdplayer-buttons.service; then
    sudo systemctl reset-failed cdplayer-buttons.service
fi
sudo systemctl enable cdplayer-buttons.service
sudo systemctl restart cdplayer-buttons.service
sleep 2
if ! systemctl is-active --quiet cdplayer-buttons.service; then
    sudo journalctl -u cdplayer-buttons.service -n 30 --no-pager
    exit 1
fi
echo 'Buttons ready: KEY1 Mode, KEY2 Play/Pause, KEY3 Next, KEY4 Previous.'
