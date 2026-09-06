#!/usr/bin/env bash
# Run after scripts/update.sh, with a compatible ALSA-enabled librespot installed.
set -Eeuo pipefail
main() {
    local receiver help_text config
    receiver="$(command -v librespot)" || { echo 'Install librespot (for example through Raspotify) first; see README.md.' >&2; exit 1; }
    help_text="$("$receiver" --help)"
    for option in --emit-sink-events --onevent --disable-credential-cache --disable-audio-cache; do
        [[ "$help_text" == *"$option"* ]] || { echo "librespot lacks $option; upgrade it first." >&2; exit 1; }
    done
    [[ -x /usr/local/bin/cdplayer ]] || { echo 'Run scripts/update.sh first.' >&2; exit 1; }
    /usr/local/bin/cdplayer -h 2>&1 | rg_check_spotify
    sudo -v
    sudo install -d -m 755 /etc/cdplayer
    if ! sudo test -f /etc/cdplayer/spotify.conf; then
        config="$(mktemp)"
        printf 'CDPLAYER_SPOTIFY_ENABLED=1\nCDPLAYER_SPOTIFY_BINARY="%s"\nCDPLAYER_SPOTIFY_NAME="Raspberry Pi CD Player"\nCDPLAYER_SPOTIFY_DEVICE="plughw:CARD=Headphones,DEV=0"\n' "$receiver" > "$config"
        sudo install -m 644 "$config" /etc/cdplayer/spotify.conf
        rm -f "$config"
    fi
    # Only one receiver may own the sound output or advertise this device.
    if systemctl cat raspotify.service >/dev/null 2>&1; then
        sudo systemctl disable --now raspotify.service
    fi
    sudo systemctl daemon-reload
    sudo systemctl enable cdplayer.service
    sudo systemctl restart cdplayer.service
    echo 'Spotify enabled. Choose Raspberry Pi CD Player in Spotify on your phone.'
    echo 'Output/name settings: /etc/cdplayer/spotify.conf; logs: journalctl -u cdplayer -f'
}
rg_check_spotify() { local help; help="$(cat)"; [[ "$help" == *"-spotify"* ]] || { echo 'Update the installed CD player first.' >&2; return 1; }; }
main "$@"
