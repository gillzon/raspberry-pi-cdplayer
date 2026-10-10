#!/usr/bin/env python3
"""Prevent MPD from opening the drive alongside the cached CD reader."""
import argparse
from pathlib import Path
import re
import shutil


def render(config, direct=False):
    enabled = 'yes' if direct else 'no'
    found = 0

    def update(match):
        nonlocal found
        body = match.group(1)
        if not re.search(r'(?m)^[ \t]*plugin[ \t]+(?:"cdio_paranoia"|cdio_paranoia)[ \t]*(?:#[^\n]*)?$', body):
            return match.group(0)
        found += 1
        pattern = r'(?m)^[ \t]*enabled[ \t]+(?:"(?:yes|no)"|yes|no)[ \t]*(?:#[^\n]*)?$'
        if len(re.findall(pattern, body)) > 1:
            raise ValueError('Duplicate cdio_paranoia enabled settings')
        if re.search(pattern, body):
            body = re.sub(pattern, f'    enabled "{enabled}"', body)
        else:
            body = body.rstrip() + f'\n    enabled "{enabled}"\n'
        return 'input {' + body + '}'

    result = re.sub(r'(?m)^[ \t]*input[ \t]*\{([^{}]*)\}', update, config)
    if found > 1:
        raise ValueError('Multiple cdio_paranoia blocks; resolve them before updating')
    if not found:
        result = result.rstrip() + f'\n\n# CD drive ownership is configured by cdplayer.\ninput {{\n    plugin "cdio_paranoia"\n    enabled "{enabled}"\n}}\n'
    pattern = r'(?m)^[ \t]*restore_paused[ \t]+(?:"(?:yes|no)"|yes|no)[ \t]*(?:#[^\n]*)?$'
    if len(re.findall(pattern, result)) > 1:
        raise ValueError('Multiple restore_paused settings')
    if re.search(pattern, result):
        result = re.sub(pattern, 'restore_paused "yes"', result)
    else:
        result = result.rstrip() + '\n\n# Let cdplayer choose the source after startup.\nrestore_paused "yes"\n'
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('config', nargs='?', default='/etc/mpd.conf', type=Path)
    parser.add_argument('--direct', action='store_true', help='re-enable direct cdda:// playback')
    args = parser.parse_args()
    old = args.config.read_text()
    new = render(old, args.direct)
    if new == old:
        return
    backup = args.config.with_name(args.config.name + '.before-cdplayer-cd')
    if not backup.exists():
        shutil.copy2(args.config, backup)
    temporary = args.config.with_name(args.config.name + '.cdplayer-cd-new')
    shutil.copy2(args.config, temporary)
    temporary.write_text(new)
    temporary.replace(args.config)
    print('MPD CD reader configuration updated; restart MPD to apply.')


if __name__ == '__main__':
    main()
