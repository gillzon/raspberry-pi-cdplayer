#!/usr/bin/env python3
"""Four playback buttons for Waveshare 2.8inch RPi LCD (A) Rev2.1 / Pi 4."""
import argparse
from contextlib import ExitStack
import json
import logging
from pathlib import Path
import queue
import signal
import threading
import time
import urllib.request

# BCM numbering; GPIO18 is deliberately not used.
BUTTONS = ((4, 'source-next'), (23, 'toggle'), (24, 'next'), (25, 'previous'))


class Press:
    def __init__(self, pressed=False):
        self.stable = self.candidate = pressed
        self.changed = 0

    def update(self, pressed, now):
        if pressed != self.candidate:
            self.candidate, self.changed = pressed, now
        if self.candidate != self.stable and now - self.changed >= .06:
            self.stable = self.candidate
            return self.stable
        return False


def send(action):
    request = urllib.request.Request('http://127.0.0.1:8080/api/control',
                                     data=json.dumps({'action': action}).encode(),
                                     headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            response.read(4096)
    except Exception as error:
        # Never retry an ambiguous command: a second mode/next could skip twice.
        logging.warning('%s failed: %s', action, error)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--rev2.1', action='store_true', required=True)
    parser.parse_args()
    if 'Raspberry Pi 4 Model B' not in Path('/proc/device-tree/model').read_text():
        parser.error('This wiring configuration targets a Raspberry Pi 4.')
    from gpiozero import DigitalInputDevice
    from gpiozero.pins.lgpio import LGPIOFactory
    stop = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stop.set())
    signal.signal(signal.SIGINT, lambda *_: stop.set())
    commands = queue.Queue(maxsize=1)
    busy = threading.Event()

    def worker():
        while not stop.is_set():
            try:
                action = commands.get(timeout=.2)
            except queue.Empty:
                continue
            try:
                send(action)
            finally:
                busy.clear()

    threading.Thread(target=worker, daemon=True).start()
    factory = LGPIOFactory(chip=0)
    try:
        with ExitStack() as stack:
            inputs = []
            for pin, action in BUTTONS:
                button = stack.enter_context(DigitalInputDevice(pin, pull_up=True, pin_factory=factory))
                inputs.append((button, Press(button.is_active), action))
            logging.info('KEY1 Mode; KEY2 Play/Pause (radio: Stop); KEY3 Next; KEY4 Previous')
            while not stop.wait(.02):
                now = time.monotonic()
                for button, press, action in inputs:
                    if press.update(button.is_active, now) and not busy.is_set():
                        busy.set()
                        commands.put_nowait(action)
    finally:
        factory.close()


if __name__ == '__main__':
    logging.basicConfig(level=logging.INFO, format='%(levelname)s %(message)s')
    main()
