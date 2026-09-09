#!/usr/bin/env python3
"""Allow large USB mixes in MPD. Backs up the configuration before editing."""
import pathlib
import re
import shutil
import sys


def render(config):
    pattern = r'(?m)^\s*max_playlist_length\s+"?(\d+)"?[^\n]*$'
    matches = list(re.finditer(pattern, config))
    if len(matches) > 1:
        raise ValueError('Multiple max_playlist_length settings; resolve them before configuring shuffle')
    if matches:
        if int(matches[0].group(1)) >= 100000:
            return config
        return re.sub(pattern, 'max_playlist_length "100000"', config)
    return config.rstrip() + '\n\n# Allow large CD player USB mixes.\nmax_playlist_length "100000"\n'


def main():
    path = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else '/etc/mpd.conf')
    old = path.read_text()
    new = render(old)
    if old == new:
        return
    backup = path.with_name(path.name + '.before-cdplayer-queue')
    if not backup.exists():
        shutil.copy2(path, backup)
    temporary = path.with_name(path.name + '.cdplayer-queue-new')
    shutil.copy2(path, temporary)
    temporary.write_text(new)
    temporary.replace(path)
    print('MPD queue limit set to 100000 songs. Restart MPD to apply.')


if __name__ == '__main__':
    main()
