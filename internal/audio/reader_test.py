"""Exercise the helper protocol with a drive that records speed/read ordering."""
import ctypes
import importlib.util
import io
from pathlib import Path
import struct
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location('reader', Path(__file__).with_name('reader.py'))
reader = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reader)


class ReaderSpeedTest(unittest.TestCase):
    def run_reader(self, speed=2, startup=8, verify=False, failure=None, rejected=False):
        calls = []
        pcm = ctypes.create_string_buffer(bytes(2352))

        def function(name):
            def call(*args):
                calls.append((name, args))
                if name == failure:
                    return 0 if name in ('cdio_cddap_identify_cdio', 'cdio_cddap_read') else -1
                if name in ('cdio_cddap_speed_set', 'cdio_set_speed'):
                    return -2 if rejected else 0
                if name == 'cdio_cddap_read':
                    return args[-1]
                if name == 'cdio_cddap_track_lastsector':
                    return 149
                if name in ('cdio_cddap_track_audiop', 'cdio_open', 'cdio_cddap_identify_cdio', 'cdio_paranoia_init'):
                    return 1
                if name == 'cdio_paranoia_read_limited':
                    return ctypes.addressof(pcm)
                return 0
            return Mock(side_effect=call)

        class Library:
            def __getattr__(self, name):
                value = function(name)
                setattr(self, name, value)
                return value

        class Output(io.BytesIO):
            def write(self, data):
                calls.append(('output', (data,)))
                return super().write(data)

        output = Output()
        with patch.object(reader.C, 'CDLL', return_value=Library()), \
             patch.object(reader.ctypes.util, 'find_library', return_value='fake'), \
             patch.object(reader, 'watch_parent'), patch.object(reader.faulthandler, 'enable'), \
             patch.object(reader, 'CD_READ_SPEED', speed, create=True), \
             patch.object(reader, 'CD_STARTUP_SPEED', startup, create=True), \
             patch.object(reader, 'VERIFY_AUDIO', verify, create=True), \
             patch.object(reader.sys, 'argv', ['reader', '/dev/fake', '[{"number":1,"start":0,"end":150}]', '1']), \
             patch.object(reader.sys, 'stdin', Mock(buffer=io.BytesIO(b'0 1\n1 1\n'))), \
             patch.object(reader.sys, 'stdout', Mock(buffer=output)), \
             patch.object(reader.sys, 'stderr', io.StringIO()):
            if failure:
                with self.assertRaises(RuntimeError):
                    reader.main()
            else:
                reader.main()
                self.assertEqual(output.getvalue(), b'CDPCM1\n' + (struct.pack('<I', 2352) + bytes(2352)) * 2)
        return calls

    def test_fast_initialization_then_quiet_before_first_audio(self):
        for verify in (False, True):
            with self.subTest(verify=verify):
                calls = self.run_reader(verify=verify)
                names = [name for name, _ in calls]
                speeds = [(name, args[-1]) for name, args in calls if name in ('cdio_set_speed', 'cdio_cddap_speed_set')]
                self.assertEqual(speeds, [('cdio_set_speed', 8), ('cdio_cddap_speed_set', 2)])
                self.assertLess(names.index('cdio_set_speed'), names.index('cdio_cddap_open'))
                read = 'cdio_paranoia_read_limited' if verify else 'cdio_cddap_read'
                self.assertLess(names.index(read), names.index('cdio_cddap_speed_set'))
                audio = next(i for i, (name, args) in enumerate(calls) if name == 'output' and args[0] != b'CDPCM1\n')
                self.assertLess(names.index('cdio_cddap_speed_set'), audio)
                self.assertEqual(names.count('cdio_cddap_close'), 1)
                self.assertNotIn('cdio_destroy', names)

    def test_disabled_speed_control_leaves_drive_untouched(self):
        calls = self.run_reader(speed=0)
        self.assertFalse(any('speed' in name for name, _ in calls))

    def test_disabled_or_lower_startup_speed_uses_only_playback_speed(self):
        for startup in (0, 1, 2):
            with self.subTest(startup=startup):
                calls = self.run_reader(startup=startup)
                self.assertEqual([(n, a[-1]) for n, a in calls if 'speed' in n], [('cdio_cddap_speed_set', 2)])

    def test_rejected_speed_requests_do_not_stop_audio(self):
        self.run_reader(rejected=True)

    def test_failures_restore_quiet_speed_and_release_handle(self):
        for failure in ('cdio_cddap_identify_cdio', 'cdio_cddap_open', 'cdio_cddap_read'):
            with self.subTest(failure=failure):
                calls = self.run_reader(failure=failure)
                self.assertEqual([(n, a[-1]) for n, a in calls if 'speed' in n], [('cdio_set_speed', 8), ('cdio_set_speed', 2)])
                expected_close = 'cdio_destroy' if failure == 'cdio_cddap_identify_cdio' else 'cdio_cddap_close'
                self.assertEqual(calls[-1][0], expected_close)


if __name__ == '__main__':
    unittest.main()
