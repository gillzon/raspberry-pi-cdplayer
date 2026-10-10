import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('cd_config', Path(__file__).with_name('configure-mpd-cd.py'))
config = importlib.util.module_from_spec(spec)
spec.loader.exec_module(config)


class CDConfigTest(unittest.TestCase):
    def test_existing_outputs_and_state_file_are_preserved(self):
        outputs = 'state_file "/var/lib/mpd/state"\naudio_output {\n type "alsa"\n name "Speakers"\n}\n'
        result = config.render(outputs)
        self.assertIn(outputs, result)
        self.assertIn('plugin "cdio_paranoia"\n    enabled "no"', result)
        self.assertIn('restore_paused "yes"', result)
        self.assertEqual(config.render(result), result)

    def test_disable_existing_reader_and_autoplay(self):
        for enabled in ('    enabled "yes"\n', ''):
            original = 'input {\n    plugin "cdio_paranoia"\n' + enabled + '}\nrestore_paused "no"\n'
            result = config.render(original)
            self.assertEqual(result.count('plugin "cdio_paranoia"'), 1)
            self.assertIn('enabled "no"', result)
            self.assertNotIn('restore_paused "no"', result)
            self.assertEqual(config.render(result), result)

    def test_preserve_other_inputs_and_comments(self):
        original = '# input {\n# plugin "cdio_paranoia"\n# enabled "yes"\n# }\ninput {\n plugin "curl"\n enabled "yes"\n}\n'
        self.assertIn(original, config.render(original))

    def test_direct_playback_can_be_restored(self):
        result = config.render(config.render(''), direct=True)
        self.assertIn('enabled "yes"', result)

    def test_reject_duplicate_settings(self):
        with self.assertRaises(ValueError):
            config.render('input {\n plugin "cdio_paranoia"\n}\n' * 2)
        with self.assertRaises(ValueError):
            config.render('restore_paused "yes"\nrestore_paused "no"\n')


if __name__ == '__main__':
    unittest.main()
