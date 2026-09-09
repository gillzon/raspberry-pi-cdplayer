#!/usr/bin/env bash
# Run on a Pi 4 only after checking the LCD's Rev2.1 marking.
set -Eeuo pipefail
if [[ "${1:-}" == --help || "${1:-}" == -h ]]; then
    echo 'Usage: bash scripts/setup-brightness-button.sh --rev2.1'
    echo 'Verify Rev2.1 on the LCD first. KEY4 will cycle 20/40/60/80/100% brightness.'
    exit 0
fi
if [[ $# != 1 || "$1" != --rev2.1 ]]; then
    echo 'Check the LCD revision, then pass --rev2.1. Older boards do not support this backlight wiring.' >&2
    exit 1
fi
if (( EUID == 0 )); then
    echo 'Run as your normal Pi user, without sudo.' >&2
    exit 1
fi
if [[ ! -r /proc/device-tree/model ]] || ! grep -aq 'Raspberry Pi 4 Model B' /proc/device-tree/model; then
    echo 'Run this installer on the Raspberry Pi 4, not the workstation.' >&2
    exit 1
fi
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
sudo apt-get update
sudo apt-get install -y python3-gpiozero python3-lgpio
sudo install -D -m 0644 "$repo_dir/scripts/brightness-button.py" /usr/local/lib/cdplayer/brightness-button.py
sudo install -m 0644 "$repo_dir/deploy/cdplayer-backlight.service" /etc/systemd/system/cdplayer-backlight.service
sudo systemctl daemon-reload
sudo systemctl enable cdplayer-backlight.service
sudo systemctl restart cdplayer-backlight.service
sleep 2
if ! systemctl is-active --quiet cdplayer-backlight.service; then
    sudo journalctl -u cdplayer-backlight.service -n 30 --no-pager
    echo 'Brightness service failed. Check GPIO ownership and the LCD revision.' >&2
    exit 1
fi
echo 'KEY4 now cycles brightness. The saved level is restored at boot; first start uses 60%.'
