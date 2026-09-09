#!/usr/bin/env python3
"""KEY4 brightness cycling for Waveshare 2.8inch RPi LCD (A) Rev2.1 / Pi 4."""
import argparse
import logging
import os
from pathlib import Path
import signal
import tempfile
import threading
import time

LEVELS = (20, 40, 60, 80, 100)
KEY_PINS = {'KEY1': 4, 'KEY2': 23, 'KEY3': 24, 'KEY4': 25}
BACKLIGHT_PIN = 18


class PressCycle:
    """Require a stable press and release; neither bounce nor holding repeats."""
    def __init__(self, level=60, pressed=False, now=0):
        self.level = level if level in LEVELS else 60
        self.stable = self.candidate = pressed
        self.changed = now

    def update(self, pressed, now):
        if pressed != self.candidate:
            self.candidate, self.changed = pressed, now
        if self.candidate != self.stable and now - self.changed >= 0.06:
            self.stable = self.candidate
            if self.stable:
                self.level = LEVELS[(LEVELS.index(self.level) + 1) % len(LEVELS)]
                return self.level
        return None


def load_level(path):
    try:
        level = int(path.read_text().strip())
        if level in LEVELS:
            return level
    except FileNotFoundError:
        pass
    except (ValueError, OSError) as error:
        logging.warning('Cannot read saved brightness: %s', error)
    return 60


def save_level(path, level):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(dir=path.parent, prefix='.brightness-')
    try:
        with os.fdopen(fd, 'w') as stream:
            stream.write(str(level) + '\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--rev2.1', dest='revision', action='store_true', required=True,
                        help='Confirm Rev2.1 is printed on the back of the LCD')
    parser.add_argument('--key', choices=KEY_PINS, default='KEY4')
    parser.add_argument('--state', type=Path, default=Path('/var/lib/cdplayer-backlight/brightness'))
    args = parser.parse_args()
    model = Path('/proc/device-tree/model')
    if not model.exists() or 'Raspberry Pi 4 Model B' not in model.read_text():
        parser.error('This wiring configuration targets a Raspberry Pi 4.')
    from gpiozero import DigitalInputDevice, PWMOutputDevice
    from gpiozero.pins.lgpio import LGPIOFactory

    stop = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stop.set())
    signal.signal(signal.SIGINT, lambda *_: stop.set())
    # lgpio software PWM avoids reprogramming the hardware PWM clock used by
    # the Pi's analog audio. The GPIO character device enforces line ownership.
    factory = LGPIOFactory(chip=0)
    try:
        with DigitalInputDevice(KEY_PINS[args.key], pull_up=True, pin_factory=factory) as button:
            cycle = PressCycle(load_level(args.state), button.is_active, time.monotonic())
            with PWMOutputDevice(BACKLIGHT_PIN, frequency=1000, initial_value=cycle.level / 100,
                                 pin_factory=factory) as backlight:
                logging.info('%s cycles brightness; restored %d%%', args.key, cycle.level)
                while not stop.wait(0.02):
                    level = cycle.update(button.is_active, time.monotonic())
                    if level is not None:
                        backlight.value = level / 100
                        logging.info('Brightness %d%%', level)
                        try:
                            save_level(args.state, level)
                        except OSError:
                            logging.exception('Brightness changed but could not be saved')
    finally:
        factory.close()


if __name__ == '__main__':
    logging.basicConfig(level=logging.INFO, format='%(levelname)s %(message)s')
    main()
