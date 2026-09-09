import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('buttons', Path(__file__).with_name('player-buttons.py'))
buttons = importlib.util.module_from_spec(spec)
spec.loader.exec_module(buttons)


class ButtonTest(unittest.TestCase):
    def test_bounce_and_hold_generate_only_one_command_until_released(self):
        press = buttons.Press()
        events = [(True, 1), (False, 1.01), (True, 1.02), (True, 1.1),
                  (True, 2), (False, 3), (False, 3.1), (True, 4), (True, 4.1)]
        self.assertEqual(sum(press.update(value, now) for value, now in events), 2)

    def test_button_held_at_boot_does_not_switch_mode(self):
        press = buttons.Press(True)
        self.assertFalse(press.update(True, 5))
        self.assertFalse(press.update(False, 6))
        self.assertFalse(press.update(False, 6.1))
        self.assertFalse(press.update(True, 7))
        self.assertTrue(press.update(True, 7.1))


if __name__ == '__main__':
    unittest.main()
