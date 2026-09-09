import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch
import tempfile
from types import SimpleNamespace

spec = importlib.util.spec_from_file_location("boot_splash", Path(__file__).with_name("configure-boot-splash.py"))
boot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(boot)


class BootConfigTest(unittest.TestCase):
    def test_new_display_config_and_rotation_preserve_existing_settings(self):
        config = boot.display_config("", "right")
        self.assertIn('Driver "fbdev"', config)
        self.assertIn('Option "fbdev" "/dev/fb0"', config)
        self.assertIn('Option "Rotate" "CW"', config)
        self.assertEqual(config, boot.display_config(config))
        self.assertEqual(config, boot.display_config(config, "right"))
        left = boot.display_config(config, "left")
        self.assertIn('Option "Rotate" "CCW"', left)
        self.assertNotIn('"CW"', left)
        self.assertNotIn('"Rotate"', boot.display_config(left, "normal"))

    def test_does_not_rewrite_unknown_display_configuration(self):
        with self.assertRaises(ValueError):
            boot.display_config('Section "Device"\nDriver "modesetting"\nEndSection\n')
        with self.assertRaises(ValueError):
            boot.display_config(boot.display_config("") + 'Section "Screen"\nEndSection\n', "right")

    def test_display_setup_preflight_accepts_console_pi_without_lightdm_or_initramfs(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name, content in {
                "proc/device-tree/model": "Raspberry Pi 4 Model B Rev 1.2",
                "sys/class/graphics/fb0/name": "fb_st7789v\n",
            }.items():
                target = root / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(content)
            def mapped_path(name):
                path = Path(name)
                return root / str(path).lstrip('/') if path.is_absolute() else path
            with patch.object(boot, 'Path', side_effect=mapped_path), \
                 patch.object(boot.os, 'uname', return_value=SimpleNamespace(machine='aarch64')), \
                 patch.object(boot.pwd, 'getpwnam', return_value=SimpleNamespace(pw_uid=1000)), \
                 patch.object(boot.subprocess, 'run') as run:
                config = boot.preflight('newpiuser', display_only=True, rotation='right')
                self.assertIn('Option "Rotate" "CW"', config)
                run.assert_called_once_with(['systemctl', 'cat', 'cdplayer.service'], check=True, stdout=boot.subprocess.DEVNULL)

    def test_preserves_root_console_and_hardware_arguments(self):
        original = "console=serial0,115200 console=tty1 root=PARTUUID=abcd-02 rootwait rw fbcon=map:0 loglevel=7 quiet splash\n"
        result = boot.boot_cmdline(original)
        for token in ("console=serial0,115200", "console=tty1", "root=PARTUUID=abcd-02", "rootwait", "rw", "fbcon=map:0"):
            self.assertIn(token, result.split())
        self.assertNotIn("loglevel=7", result)
        self.assertEqual(result, boot.boot_cmdline(result))

    def test_rejects_invalid_boot_command_line_before_writing(self):
        for original in ("", "quiet splash", "root=/dev/mmcblk0p2\nsplash\n"):
            with self.assertRaises(ValueError):
                boot.boot_cmdline(original)

    def test_lightdm_update_preserves_other_seats_and_comments(self):
        original = "# User configuration\n[Seat:*]\n# autologin-session=example\nautologin-session=LXDE-pi-x\nxserver-command=X -s 0\n[Seat:remote]\nautologin-session=remote\n"
        result = boot.set_ini(original, "Seat:*", {"autologin-session": "cdplayer-kiosk"})
        self.assertIn("# User configuration", result)
        self.assertIn("# autologin-session=example", result)
        self.assertIn("xserver-command=X -s 0", result)
        self.assertIn("[Seat:remote]\nautologin-session=remote", result)
        self.assertNotIn("autologin-session=LXDE-pi-x", result)
        self.assertEqual(result, boot.set_ini(result, "Seat:*", {"autologin-session": "cdplayer-kiosk"}))

    def test_adds_missing_section_without_changing_existing_settings(self):
        original = "[LightDM]\nminimum-vt=7\n"
        result = boot.set_ini(original, "Seat:*", {"autologin-session": "cdplayer-kiosk"})
        self.assertTrue(result.startswith(original))
        self.assertIn("[Seat:*]\nautologin-session=cdplayer-kiosk", result)

    def test_matches_existing_display_rotation(self):
        template = "angle=@ROTATION@; swap=@TURNED@;"
        self.assertEqual(boot.splash_script(template, '# Option "Rotate" "CW"'), "angle=0; swap=0;")
        for rotation, angle, turned in (("CW", boot.math.pi / 2, 1), ("CCW", -boot.math.pi / 2, 1), ("UD", boot.math.pi, 0)):
            self.assertEqual(boot.splash_script(template, f' Option "Rotate" "{rotation}"'), f"angle={angle}; swap={turned};")


if __name__ == "__main__":
    unittest.main()
