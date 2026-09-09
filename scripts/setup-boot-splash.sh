#!/usr/bin/env bash
# Run from the Pi checkout as its desktop auto-login user, without sudo.
set -Eeuo pipefail
repo_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
if (( EUID == 0 )); then
    echo "Run as the Pi's desktop user, without sudo." >&2
    exit 1
fi
# Check the actual target before building or requesting privilege. In particular,
# do not reconfigure the developer's workstation by mistake.
python3 scripts/configure-boot-splash.py --check --user "$(id -un)"
build_dir="$(mktemp -d)"
trap 'rm -rf -- "$build_dir"' EXIT
go test ./internal/web
go build -trimpath -o "$build_dir/cdplayer" ./cmd/cdplayer
sudo python3 scripts/configure-boot-splash.py \
    --user "$(id -un)" --binary "$build_dir/cdplayer"
