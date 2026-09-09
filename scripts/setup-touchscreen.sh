#!/usr/bin/env bash
# Persist the confirmed working ADS7846 settings for the rotated X11 display.
set -Eeuo pipefail
if [[ "${1:-}" == --help ]]; then
    echo 'Usage: bash scripts/setup-touchscreen.sh'
    echo 'Save calibration 198 3679 292 3800, no axis swap, and inverted Y.'
    exit 0
fi
if (( $# != 0 )); then
    echo 'Usage: bash scripts/setup-touchscreen.sh' >&2
    exit 1
fi
if [[ ! -r /proc/device-tree/model ]] || ! grep -aq 'Raspberry Pi 4 Model B' /proc/device-tree/model; then
    echo 'Run this on the Raspberry Pi 4 with the ADS7846 touchscreen.' >&2
    exit 1
fi
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
config=/etc/X11/xorg.conf.d/99-z-cdplayer-touch.conf
if [[ -f "$config" ]] && ! cmp -s "$repo_dir/deploy/xorg/99-z-cdplayer-touch.conf" "$config"; then
    sudo cp -an -- "$config" "$config.before-cdplayer"
fi
sudo install -D -m 0644 "$repo_dir/deploy/xorg/99-z-cdplayer-touch.conf" "$config"
echo 'Touch calibration saved. Reboot once to apply it; no more xinput commands at startup.'
