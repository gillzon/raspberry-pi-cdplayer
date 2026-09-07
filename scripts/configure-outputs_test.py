import importlib.util
import pathlib
import tempfile
import unittest
spec = importlib.util.spec_from_file_location('outputs', pathlib.Path(__file__).with_name('configure-outputs.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class OutputSetupTests(unittest.TestCase):
    def test_preserves_config_and_is_idempotent(self):
        with tempfile.TemporaryDirectory() as temp:
            root = pathlib.Path(temp)
            for i, name in enumerate(['vc4hdmi0', 'Headphones', 'USB']):
                card = root / f'card{i}'
                (card / 'pcm0p').mkdir(parents=True)
                (card / 'id').write_text(name)
                (card / 'pcm0p/info').write_text('')
            for original in ['port "6600"\n', 'audio_output {\n type "alsa"\n name "Existing"\n}\n']:
                result = module.render(original, root)
                self.assertIn(original.strip(), result)
                self.assertIn('HDMI 1 [plughw:CARD=vc4hdmi0,DEV=0]', result)
                self.assertIn('3.5 mm headphones', result)
                self.assertIn('USB [plughw:CARD=USB,DEV=0]', result)
                self.assertEqual(result, module.render(result, root))
                self.assertEqual(result.count('enabled "no"'), 3)
    def test_no_devices_leaves_configuration_alone(self):
        with tempfile.TemporaryDirectory() as temp:
            with self.assertRaises(RuntimeError):
                module.render('port "6600"', pathlib.Path(temp))

if __name__ == '__main__':
    unittest.main()
