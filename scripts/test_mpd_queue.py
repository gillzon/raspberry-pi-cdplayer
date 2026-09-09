import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('queue_config', Path(__file__).with_name('configure-mpd-queue.py'))
config = importlib.util.module_from_spec(spec)
spec.loader.exec_module(config)


class QueueConfigTest(unittest.TestCase):
    def test_default_or_small_limit_is_raised_without_changing_outputs(self):
        for setting in ['', '#max_playlist_length "16384"\n', 'max_playlist_length "16384"\n']:
            output = 'audio_output {\n type "alsa"\n name "Headphones"\n}\n'
            result = config.render(setting + output)
            self.assertIn(output, result)
            self.assertIn('max_playlist_length "100000"', result)
            self.assertEqual(config.render(result), result)

    def test_larger_existing_limit_is_preserved(self):
        original = 'max_playlist_length "200000"\n'
        self.assertEqual(config.render(original), original)

    def test_conflicting_settings_are_rejected(self):
        with self.assertRaises(ValueError):
            config.render('max_playlist_length 10\nmax_playlist_length 20\n')
