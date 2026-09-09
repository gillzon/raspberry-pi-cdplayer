#!/usr/bin/env python3
"""Install the Comreact boot/kiosk assets on the configured Waveshare Pi 4."""

import argparse
import datetime
import json
import math
import os
from pathlib import Path
import pwd
import re
import shutil
import subprocess
import tempfile
import time
import urllib.request


REPO = Path(__file__).resolve().parent.parent
BOOT = Path("/boot/firmware")


def boot_cmdline(text):
    """Preserve root, console and hardware options; replace only splash options."""
    lines = [line for line in text.splitlines() if line.strip()]
    if len(lines) != 1 or not any(t.startswith("root=") for t in lines[0].split()):
        raise ValueError("Expected one kernel command line containing root=.")
    options = {
        "quiet": "quiet", "splash": "splash", "logo.nologo": "logo.nologo",
        "loglevel": "loglevel=3", "vt.global_cursor_default": "vt.global_cursor_default=0",
        "plymouth.ignore-serial-consoles": "plymouth.ignore-serial-consoles",
    }
    tokens = [t for t in lines[0].split() if t.split("=", 1)[0] not in options]
    return " ".join(tokens + list(options.values())) + "\n"


def set_ini(text, section, settings):
    """Update one section without removing comments or unrelated settings."""
    lines = text.splitlines()
    result = []
    active = False
    found = False
    for line in lines:
        match = re.match(r"\s*\[([^]]+)\]\s*$", line)
        if match:
            if active:
                result.extend(f"{k}={v}" for k, v in settings.items())
            active = match[1] == section
            found |= active
        if active and re.match(r"\s*(" + "|".join(map(re.escape, settings)) + r")\s*=", line):
            continue
        result.append(line)
    if not found:
        result.extend(["", f"[{section}]"])
    if active or not found:
        result.extend(f"{k}={v}" for k, v in settings.items())
    return "\n".join(result) + "\n"


def splash_script(template, xorg):
    match = re.search(r'^\s*Option\s+"Rotate"\s+"(CW|CCW|UD)"', xorg, re.M | re.I)
    rotation = match[1].upper() if match else ""
    angle = {"": 0, "CW": math.pi / 2, "CCW": -math.pi / 2, "UD": math.pi}[rotation]
    return template.replace("@ROTATION@", str(angle)).replace("@TURNED@", "1" if rotation in ("CW", "CCW") else "0")


def preflight(user):
    model = Path("/proc/device-tree/model")
    if not model.exists() or "Raspberry Pi 4 Model B" not in model.read_text():
        raise ValueError("Run on the Raspberry Pi 4, not the workstation.")
    if os.uname().machine != "aarch64":
        raise ValueError("This installer targets the Pi's 64-bit Raspberry Pi OS setup.")
    framebuffer = Path("/sys/class/graphics/fb0/name")
    if not framebuffer.exists() or framebuffer.read_text().strip() != "fb_st7789v":
        raise ValueError("Expected the working Waveshare LCD at /dev/fb0 (fb_st7789v).")
    account = pwd.getpwnam(user)
    if account.pw_uid == 0 or not re.fullmatch(r"[a-z_][a-z0-9_-]*", user):
        raise ValueError("Select the normal desktop auto-login user, e.g. gillzon.")
    xorg = Path("/etc/X11/xorg.conf.d/98-spi-screen.conf").read_text()
    if not re.search(r'Driver\s+"fbdev"', xorg, re.I) or '/dev/fb0' not in xorg:
        raise ValueError("Configure and verify the LCD's X11 fbdev session first.")
    boot_cmdline((BOOT / "cmdline.txt").read_text())
    if not (BOOT / "initramfs8").is_file():
        raise ValueError("Expected the Pi 4's automatic initramfs at /boot/firmware/initramfs8.")
    if not (REPO / "comreact-logo-WHITE.png").read_bytes().startswith(b"\x89PNG\r\n\x1a\n"):
        raise ValueError("The root logo must be a PNG file.")
    subprocess.run(["systemctl", "is-active", "--quiet", "cdplayer"], check=True)
    subprocess.run(["systemctl", "is-active", "--quiet", "lightdm"], check=True)
    return xorg


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--user", required=True)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--binary", type=Path)
    args = parser.parse_args()
    xorg = preflight(args.user)
    if args.check:
        print("Pi 4, Waveshare framebuffer, X11 configuration and services verified.")
        return
    if os.geteuid() != 0 or args.binary is None:
        parser.error("Use bash scripts/setup-boot-splash.sh as the desktop user.")
    binary = args.binary.read_bytes()
    if not binary.startswith(b"\x7fELF") or b"cdplayer:display-ready" not in binary:
        raise ValueError("Build the updated player with display readiness support first.")

    subprocess.run(["apt-get", "update"], check=True)
    subprocess.run(["apt-get", "install", "-y", "plymouth", "plymouth-themes",
                    "initramfs-tools", "feh", "openbox", "x11-xserver-utils", "chromium"], check=True)

    backup = Path(tempfile.mkdtemp(prefix=datetime.datetime.now().strftime("%Y%m%d-%H%M%S-"),
                                  dir=make_backup_dir()))
    manifest = {}

    def save(path):
        key = str(path)
        if key in manifest:
            return
        manifest[key] = path.exists() or path.is_symlink()
        if manifest[key]:
            target = backup / path.relative_to("/")
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, target, follow_symlinks=False)
        (backup / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")

    def write(path, content, mode=0o644):
        save(path)
        path.parent.mkdir(parents=True, exist_ok=True)
        # Replace atomically, including the running executable.
        fd, temporary = tempfile.mkstemp(dir=path.parent, prefix=".cdplayer-")
        try:
            with os.fdopen(fd, "wb") as stream:
                stream.write(content.encode() if isinstance(content, str) else content)
                os.fchmod(stream.fileno(), mode)
            os.replace(temporary, path)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)

    assets = REPO / "deploy/boot"
    logo = (REPO / "comreact-logo-WHITE.png").read_bytes()
    print(f"Configuration backup: {backup}", flush=True)
    for name, content in {
        "logo.png": logo, "cdplayer.plymouth": (assets / "cdplayer.plymouth").read_bytes(),
        "cdplayer.script": splash_script((assets / "cdplayer.script").read_text(), xorg),
    }.items():
        write(Path("/usr/share/plymouth/themes/cdplayer") / name, content)
    write(Path("/usr/share/cdplayer/boot/logo.png"), logo)
    write(Path("/usr/share/cdplayer/boot/start.html"), (assets / "start.html").read_bytes())
    write(Path("/usr/local/bin/cdplayer-kiosk-session"), (assets / "cdplayer-kiosk-session").read_bytes(), 0o755)
    write(Path("/usr/share/xsessions/cdplayer-kiosk.desktop"), (assets / "cdplayer-kiosk.desktop").read_bytes())

    # LightDM reads its main file after drop-ins. A seat0 section also overrides
    # generic Seat:* settings, so update both if the latter is already present.
    lightdm = Path("/etc/lightdm/lightdm.conf")
    config = lightdm.read_text() if lightdm.exists() else ""
    settings = {"autologin-user": args.user, "autologin-user-timeout": "0", "autologin-session": "cdplayer-kiosk"}
    config = set_ini(config, "Seat:*", settings)
    if re.search(r"^\s*\[Seat:seat0\]\s*$", config, re.M):
        config = set_ini(config, "Seat:seat0", settings)
    # Activate this session only after the rebuilt application passes its check.
    lightdm_config = config
    write(BOOT / "cmdline.txt", boot_cmdline((BOOT / "cmdline.txt").read_text()))
    config_txt = (BOOT / "config.txt").read_text()
    marker = "# Comreact boot splash (managed by setup-boot-splash.sh)"
    if marker not in config_txt:
        write(BOOT / "config.txt", config_txt.rstrip() + f"\n\n[all]\n{marker}\nauto_initramfs=1\ndisable_splash=1\n")
    modules = Path("/etc/initramfs-tools/modules")
    module_text = modules.read_text() if modules.exists() else ""
    for module in ("spi_bcm2835", "fb_st7789v"):
        if not re.search(r"^" + module + r"(?:\s|$)", module_text, re.M):
            module_text = module_text.rstrip() + "\n" + module + "\n"
    write(modules, module_text)
    plymouth_config = Path("/etc/plymouth/plymouthd.conf")
    text = plymouth_config.read_text() if plymouth_config.exists() else ""
    write(plymouth_config, set_ini(text, "Daemon", {"Theme": "cdplayer", "ShowDelay": "0", "DeviceTimeout": "8"}))
    subprocess.run(["plymouth-set-default-theme", "cdplayer"], check=True)
    subprocess.run(["update-initramfs", "-u", "-k", "all"], check=True)
    # Verify the image the Pi's firmware will actually load, not only /boot's
    # intermediate initrd. Raspberry Pi OS's initramfs hook must copy it here.
    contents = subprocess.check_output(["lsinitramfs", str(BOOT / "initramfs8")], text=True).splitlines()
    for name in ("logo.png", "cdplayer.plymouth", "cdplayer.script"):
        if not any(line.endswith("/plymouth/themes/cdplayer/" + name) for line in contents):
            raise RuntimeError(f"Boot initramfs is missing {name}; check the Raspberry Pi OS initramfs hook. Backup: {backup}")

    write(Path("/usr/local/bin/cdplayer"), binary, 0o755)
    subprocess.run(["systemctl", "enable", "cdplayer", "lightdm"], check=True)
    subprocess.run(["systemctl", "restart", "cdplayer"], check=True)
    # Verify the actual running application, not just the binary on disk.
    for attempt in range(20):
        try:
            with urllib.request.urlopen("http://localhost:8080/display", timeout=2) as response:
                if b"cdplayer:display-ready" in response.read():
                    break
        except OSError:
            pass
        time.sleep(0.5)
    else:
        raise RuntimeError(f"Updated display did not respond. Backup: {backup}")
    write(lightdm, lightdm_config)
    print("Installed. Run sudo reboot on the Pi to test the splash and kiosk handoff.")
    print(f"To undo: sudo python3 {REPO / 'scripts/restore-boot-splash.py'} {backup}")


def make_backup_dir():
    path = Path("/var/lib/cdplayer-boot-backups")
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    return path


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError, RuntimeError) as error:
        raise SystemExit(f"Boot splash setup failed: {error}")
