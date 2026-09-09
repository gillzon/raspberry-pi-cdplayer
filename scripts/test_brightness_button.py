import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('brightness', Path(__file__).with_name('brightness-button.py'))
brightness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(brightness)


class BrightnessTest(unittest.TestCase):
    def test_cycles_and_wraps_once_per_press_despite_bounce_and_hold(self):
        cycle = brightness.PressCycle(20)
        levels = []
        for start in range(1, 7):
            for pressed, offset in ((True, 0), (False, .01), (True, .02), (True, .1),
                                    (True, .3), (True, .5), (False, .6), (True, .61),
                                    (False, .62), (False, .7)):
                value = cycle.update(pressed, start + offset)
                if value is not None:
                    levels.append(value)
        self.assertEqual(levels, [40, 60, 80, 100, 20, 40])

    def test_held_button_at_start_does_not_change_brightness(self):
        cycle = brightness.PressCycle(80, pressed=True)
        self.assertIsNone(cycle.update(True, 10))
        self.assertIsNone(cycle.update(False, 11))
        self.assertIsNone(cycle.update(False, 11.1))
        self.assertIsNone(cycle.update(True, 12))
        self.assertEqual(cycle.update(True, 12.1), 100)

    def test_saved_level_survives_restart_and_bad_state_defaults_to_visible_level(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'brightness'
            self.assertEqual(brightness.load_level(path), 60)
            brightness.save_level(path, 100)
            self.assertEqual(brightness.PressCycle(brightness.load_level(path)).level, 100)
            path.write_text('0')
            self.assertEqual(brightness.load_level(path), 60)
            path.write_text('truncated')
            with self.assertLogs(level='WARNING'):
                self.assertEqual(brightness.load_level(path), 60)


if __name__ == '__main__':
    unittest.main()
