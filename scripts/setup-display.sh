#!/usr/bin/env bash
# Set up graphical auto-login and the player on a Pi with its LCD driver loaded.
# Run as the desired desktop user: bash scripts/setup-display.sh [--rotate right]
set -Eeuo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
exec bash "$script_dir/setup-boot-splash.sh" --display-only "$@"
