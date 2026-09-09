#!/usr/bin/env python3
"""Follow the player's screen sleep state using its existing kernel backlight."""
import argparse
import json
import logging
from pathlib import Path
import signal
import threading
import urllib.request

BACKLIGHT = Path('/sys/class/backlight/rpi_backlight/brightness')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--wake', action='store_true')
    args = parser.parse_args()
    if args.wake:
        BACKLIGHT.write_text('1\n')
        return
    if (BACKLIGHT.parent / 'max_brightness').read_text().strip() != '1':
        raise RuntimeError('Expected the on/off rpi_backlight driver')
    stop = threading.Event()
    for sig in (signal.SIGTERM, signal.SIGINT):
        signal.signal(sig, lambda *_: stop.set())
    last_error = ''
    try:
        while not stop.is_set():
            level = '1'
            try:
                with urllib.request.urlopen('http://127.0.0.1:8080/api/status', timeout=3) as response:
                    state = json.load(response)
                level = '0' if state.get('screen_asleep') is True else '1'
                last_error = ''
            except Exception as error:
                if str(error) != last_error:
                    logging.warning('Player unavailable; waking display: %s', error)
                    last_error = str(error)
            if BACKLIGHT.read_text().strip() != level:
                BACKLIGHT.write_text(level + '\n')
            stop.wait(.5)
    finally:
        BACKLIGHT.write_text('1\n')


if __name__ == '__main__':
    logging.basicConfig(level=logging.INFO)
    main()
