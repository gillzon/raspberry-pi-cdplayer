#!/usr/bin/env bash
# Run on the Pi after a failure: sudo bash scripts/diagnose-cd.sh
# Do not probe/open the drive: even a read-only ioctl can delay a busy reader.
set -uo pipefail

section() { printf '\n%s\n' "$1"; }
section 'Installed service identity and device restrictions'
if command -v systemctl >/dev/null 2>&1; then
    systemctl show cdplayer.service --no-pager \
        --property=ActiveState,SubState,User,Group,SupplementaryGroups,MainPID,PrivateDevices,DevicePolicy,DeviceAllow
    service_pid="$(systemctl show cdplayer.service --property=MainPID --value)"
    if [[ "$service_pid" =~ ^[1-9][0-9]*$ && -r "/proc/$service_pid/status" ]]; then
        section 'Actual running process identity (numeric IDs)'
        sed -n '/^\(Name\|Pid\|Uid\|Gid\|Groups\):/p' "/proc/$service_pid/status"
    fi
fi
section 'Account and optical-drive group'
id cdplayer || true
getent group cdrom || true

section 'Optical devices and current permissions'
shopt -s nullglob
optical_count=0
for block_path in /sys/class/block/*; do
    [[ -r "$block_path/device/type" ]] || continue
    read -r device_type < "$block_path/device/type"
    [[ "$device_type" == 5 ]] || continue
    ((optical_count+=1))
    device_node="/dev/${block_path##*/}"
    stat -c '%n permissions=%A mode=%a owner=%U(%u) group=%G(%g)' "$device_node" || true
    readlink -f "$block_path/device"
    if command -v getfacl >/dev/null 2>&1; then
        getfacl --absolute-names "$device_node" || true
    fi
    for sg_path in "$block_path"/device/scsi_generic/*; do
        stat -c '%n permissions=%A mode=%a owner=%U(%u) group=%G(%g)' "/dev/${sg_path##*/}" || true
    done
done
if ((optical_count == 0)); then
    echo 'No optical drive currently registered in sysfs.'
fi

section 'Recent kernel messages (USB resets, power and drive errors)'
if command -v journalctl >/dev/null 2>&1; then
    journalctl -k -b --since '10 minutes ago' -n 200 --no-pager
    section 'Recent player and MPD messages'
    journalctl -u cdplayer -u mpd --since '10 minutes ago' -n 150 --no-pager
fi
printf '\nNo devices were opened and no permissions or services were changed.\n'
