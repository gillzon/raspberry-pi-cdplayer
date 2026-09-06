#!/usr/bin/env bash
# Read-only diagnostics. Run on the Pi immediately after reproducing skipping.
set -uo pipefail

section() { printf '\n%s\n' "$1"; }
section 'Receiver version'
if command -v librespot >/dev/null 2>&1; then
    librespot --version
else
    echo 'librespot is not on PATH; check the binary configured for cdplayer.'
fi
section 'Player and receiver services'
if command -v systemctl >/dev/null 2>&1; then
    systemctl show cdplayer.service raspotify.service mpd.service cdplayer-mpd.service \
        --property=Id,ActiveState,SubState,User,SupplementaryGroups --no-pager
fi
section 'Running player processes (names only)'
if command -v pgrep >/dev/null 2>&1; then
    pgrep -l -x 'cdplayer|librespot|mpd' || true
fi
section 'ALSA hardware outputs'
if command -v aplay >/dev/null 2>&1; then
    aplay -l
else
    echo 'aplay unavailable (provided by alsa-utils).'
fi
section 'Service account groups'
id cdplayer
section 'Sound-device owners'
if command -v fuser >/dev/null 2>&1; then
    shopt -s nullglob
    pcm_nodes=(/dev/snd/pcm*p)
    if ((${#pcm_nodes[@]})); then
        fuser -v "${pcm_nodes[@]}" || true
    else
        echo 'No ALSA playback device nodes found.'
    fi
else
    echo 'fuser unavailable (provided by psmisc).'
fi
section 'Configured Spotify output (no credentials)'
if [[ -r /etc/cdplayer/spotify.conf ]]; then
    # Do not source configuration or print arbitrary environment variables.
    sed -n '/^CDPLAYER_SPOTIFY_\(ENABLED\|BINARY\|DEVICE\)=/p' /etc/cdplayer/spotify.conf
fi
section 'Recent receiver and player logs'
if command -v journalctl >/dev/null 2>&1; then
    journalctl -u cdplayer -u raspotify --since '5 minutes ago' -n 250 --no-pager
fi
printf '\nNo services or audio settings were changed. Review logs before sharing.\n'
