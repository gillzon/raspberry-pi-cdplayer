#!/usr/bin/env python3
"""Append/update managed MPD outputs without altering existing configuration.
Run as root on the Pi, then restart MPD. Also used by update.sh.
"""
import pathlib
import re
import shutil
import sys

BEGIN = '# BEGIN CDPLAYER OUTPUTS'
END = '# END CDPLAYER OUTPUTS'

def render(config, proc=pathlib.Path('/proc/asound')):
    outputs = []
    for pcm in sorted(proc.glob('card[0-9]*/pcm*p/info')):
        card = (pcm.parent.parent / 'id').read_text().strip()
        device = re.fullmatch(r'pcm(\d+)p', pcm.parent.name).group(1)
        if not re.fullmatch(r'[A-Za-z0-9_-]+', card):
            continue
        label = {'Headphones': '3.5 mm headphones', 'vc4hdmi0': 'HDMI 1', 'vc4hdmi1': 'HDMI 2'}.get(card, card)
        if device != '0':
            label += ' output ' + device
        alsa = f'plughw:CARD={card},DEV={device}'
        outputs.append(f'''audio_output {{
    type "alsa"
    name "CD Player: {label} [{alsa}]"
    device "{alsa}"
    mixer_type "software"
    enabled "no"
}}
''')
    if not outputs:
        raise RuntimeError('No ALSA playback devices found; MPD configuration unchanged')
    if config.count(BEGIN) != config.count(END) or config.count(BEGIN) > 1:
        raise RuntimeError('Invalid managed output markers; MPD configuration unchanged')
    config = re.sub(re.escape(BEGIN) + r'.*?' + re.escape(END) + r'\n?', '', config, flags=re.S)
    # Keep auto-detection functional when MPD had no explicit outputs.
    if not re.search(r'^\s*audio_output\s*\{', config, re.M):
        outputs.insert(0, 'audio_output {\n    type "alsa"\n    name "System default"\n    device "default"\n    mixer_type "software"\n}\n')
    return config.rstrip() + '\n\n' + BEGIN + '\n' + '\n'.join(outputs) + END + '\n'

def main():
    path = pathlib.Path(sys.argv[1] if len(sys.argv)>1 else '/etc/mpd.conf')
    old = path.read_text()
    new = render(old)
    if new != old:
        backup = path.with_name(path.name + '.before-cdplayer-outputs')
        if not backup.exists():
            shutil.copy2(path, backup)
        temporary = path.with_name(path.name + '.cdplayer-new')
        shutil.copy2(path, temporary)
        temporary.write_text(new)
        temporary.replace(path)
        print('Audio outputs configured; restart MPD to load them.')

if __name__ == '__main__':
    main()
