import importlib.util
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('screen', Path(__file__).with_name('screen-sleep.py'))
screen = importlib.util.module_from_spec(spec)
spec.loader.exec_module(screen)


class Stop:
    def __init__(self): self.count = 0
    def is_set(self): return self.count >= 2
    def wait(self, _): self.count += 1


class ScreenTest(unittest.TestCase):
    def test_sleep_then_connection_failure_restores_light(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'max_brightness').write_text('1')
            light = root / 'brightness'
            light.write_text('1')
            writes = []
            original = Path.write_text
            def write(path, value):
                writes.append(value.strip())
                return original(path, value)
            with patch.object(screen, 'BACKLIGHT', light), patch('sys.argv', ['screen']), \
                 patch.object(screen.signal, 'signal'), patch.object(screen.threading, 'Event', return_value=Stop()), \
                 patch.object(screen.urllib.request, 'urlopen', side_effect=[io.StringIO('{"screen_asleep":true}'), OSError('offline')]), \
                 patch.object(Path, 'write_text', write), self.assertLogs(level='WARNING'):
                screen.main()
            self.assertEqual(writes, ['0', '1', '1'])
            self.assertEqual(light.read_text().strip(), '1')


if __name__ == '__main__': unittest.main()
