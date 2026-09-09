#!/usr/bin/env bash
# Run from the Pi checkout as its desktop auto-login user, without sudo.
set -Eeuo pipefail
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
for argument in "$@"; do
    if [[ "$argument" == --help || "$argument" == -h ]]; then
        echo "Usage: bash scripts/setup-boot-splash.sh [--rotate normal|right|left|inverted]"
        echo "Display auto-login only: bash scripts/setup-display.sh [--rotate ...]"
        echo "Run on the Pi as the normal user who should log in automatically."
        exit 0
    fi
done
if (( EUID == 0 )); then
    echo "Run as the Pi's desktop user, without sudo." >&2
    exit 1
fi
# Check the actual target before building or requesting privilege. In particular,
# do not reconfigure the developer's workstation by mistake.
python3 scripts/configure-boot-splash.py --check --user "$(id -un)" "$@"
build_dir="$(mktemp -d)"
trap 'rm -rf -- "$build_dir"' EXIT
go test ./internal/web
go build -trimpath -o "$build_dir/cdplayer" ./cmd/cdplayer
sudo python3 scripts/configure-boot-splash.py \
    --user "$(id -un)" --binary "$build_dir/cdplayer" "$@"
