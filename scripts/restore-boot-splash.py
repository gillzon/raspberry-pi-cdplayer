#!/usr/bin/env python3
"""Restore files from the backup printed by setup-boot-splash.sh."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

if os.geteuid() != 0 or len(sys.argv) != 2:
    raise SystemExit("Usage: sudo python3 scripts/restore-boot-splash.py BACKUP_DIRECTORY")
backup = Path(sys.argv[1]).resolve()
if backup.parent != Path("/var/lib/cdplayer-boot-backups"):
    raise SystemExit("Select a backup under /var/lib/cdplayer-boot-backups.")
manifest = json.loads((backup / "manifest.json").read_text())
for name, existed in manifest.items():
    path = Path(name)
    if existed:
        source = backup / path.relative_to("/")
        # The application can still be running; replace rather than truncate it.
        temporary = path.with_name(path.name + ".cdplayer-restore")
        temporary.unlink(missing_ok=True)
        shutil.copy2(source, temporary, follow_symlinks=False)
        os.replace(temporary, path)
    else:
        path.unlink(missing_ok=True)
subprocess.run(["systemctl", "daemon-reload"], check=True)
if "/etc/initramfs-tools/modules" in manifest:
    subprocess.run(["update-initramfs", "-u", "-k", "all"], check=True)
print("Previous configuration restored. Run sudo reboot.")
