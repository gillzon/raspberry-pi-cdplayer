"""USB integration smoke test with generated MP3s and a simulated MPD.

Usage: python3 scripts/test-usb.py /path/to/built/cdplayer
Requires ffmpeg, python3-mutagen, and permission to open localhost sockets.
"""
import json
from pathlib import Path
import shlex
import shutil
import socket
import subprocess
import tempfile
import sys
import threading
import time
import urllib.request


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


if len(sys.argv) != 2:
    raise SystemExit('Usage: python3 scripts/test-usb.py /path/to/built/cdplayer')

with tempfile.TemporaryDirectory() as tmp:
    root = Path(tmp) / 'music'
    root.mkdir()
    for number, title in [(1, 'First Song'), (2, 'Second Song')]:
        subprocess.run(['ffmpeg', '-loglevel', 'error', '-f', 'lavfi', '-i',
                        'sine=frequency=440:duration=1', '-metadata', 'artist=Test Artist',
                        '-metadata', 'album=Test Album', '-metadata', 'title='+title,
                        '-metadata', 'track='+str(number), str(root / (str(number)+'.mp3'))], check=True)
    listener = socket.socket()
    listener.bind(('127.0.0.1', 0))
    listener.listen()
    queue = []
    state = {'state': 'stop'}
    commands = []
    def serve():
        conn, _ = listener.accept()
        with conn, conn.makefile('rwb', buffering=0) as stream:
            stream.write(b'OK MPD 0.23.0\n')
            while line := stream.readline():
                command = line.decode().strip()
                commands.append(command)
                fields = shlex.split(command)
                if fields[0] == 'add':
                    queue.append(fields[1])
                elif fields[0] == 'clear':
                    queue.clear()
                    state.pop('song', None)
                elif fields[0] == 'play':
                    state['state'] = 'play'
                    if len(fields) > 1:
                        state['song'] = fields[1]
                elif fields[0] == 'pause':
                    state['state'] = 'pause'
                elif fields[0] == 'stop':
                    state['state'] = 'stop'
                elif fields[0] in ('next', 'previous'):
                    state['song'] = str(int(state.get('song', 0)) + (1 if fields[0]=='next' else -1))
                if command == 'status':
                    stream.write((''.join(k+': '+v+'\n' for k,v in state.items())+'duration: 1\nelapsed: 0.2\nOK\n').encode())
                else:
                    stream.write(b'OK\n')
    threading.Thread(target=serve, daemon=True).start()
    http_port = port()
    base = 'http://127.0.0.1:'+str(http_port)
    log = open(Path(tmp)/'app.log', 'w+')
    app = subprocess.Popen([sys.argv[1], '-audio-cache=false', '-music-dir='+str(root),
                            '-cache-dir='+tmp+'/cache', '-http=127.0.0.1:'+str(http_port),
                            '-mpd=127.0.0.1:'+str(listener.getsockname()[1])], stdout=log, stderr=log)
    def get(path):
        return json.load(urllib.request.urlopen(base+path, timeout=5))
    def control(action, **kwargs):
        body = json.dumps(dict(action=action, **kwargs)).encode()
        req = urllib.request.Request(base+'/api/control', data=body, headers={'Content-Type':'application/json'})
        with urllib.request.urlopen(req, timeout=10) as r:
            assert r.status == 204
    try:
        for _ in range(100):
            try:
                result = get('/api/library?q=test%20artist%20album')
                if result['total']==2:
                    break
            except Exception:
                pass
            time.sleep(.1)
        else:
            raise AssertionError('library did not scan')
        assert result['tracks'][0]['duration'] > .9
        control('usb-play', song_id=result['tracks'][1]['id'])
        status = get('/api/status')
        assert status['source']=='usb' and status['usb']['title']=='Second Song', status
        assert len(queue)==2
        req = urllib.request.Request(queue[1], headers={'Range':'bytes=0-9'})
        with urllib.request.urlopen(req) as r:
            assert r.status==206 and len(r.read())==10
        control('pause')
        assert get('/api/status')['mpd']['state']=='pause'
        control('previous')
        time.sleep(1.1)
        assert get('/api/status')['usb']['title']=='First Song'
        control('next')
        time.sleep(1.1)
        assert get('/api/status')['usb']['title']=='Second Song'
        control('stop')
        assert get('/api/status')['source']=='usb'
        (root/'2.mp3').unlink()
        try:
            urllib.request.urlopen(queue[1])
            raise AssertionError('removed music still served')
        except urllib.error.HTTPError as error:
            assert error.code==404
        control('play')
        control('source-cd')
        assert get('/api/status')['source']=='cd'
        assert not queue and state['state']=='stop', 'USB audio continued after switching to absent CD'
        for number in range(3, 47):
            shutil.copyfile(root/'1.mp3', root/(str(number)+'.mp3'))
        req = urllib.request.Request(base+'/api/library/refresh', data=b'')
        urllib.request.urlopen(req, timeout=5).close()
        for _ in range(100):
            result = get('/api/library')
            if result['total'] == 45:
                break
            time.sleep(.1)
        else:
            raise AssertionError('expanded library did not scan')
        assert len(result['tracks']) == 20
        assert len(get('/api/library?offset=20')['tracks']) == 20
        assert len(get('/api/library?offset=40')['tracks']) == 5
        control('usb-mix')
        status = get('/api/status')
        assert status['source']=='usb' and status['usb_mix']
        assert len(queue)==45 and len(set(queue))==45, 'Mix all omitted or duplicated songs'
        assert status['usb']['id']+'.mp3' in queue[0]
        print('PASS: Mix all across 45 songs, 20/20/5 pagination, real MP3 index/search/duration, selected album song, HTTP range streaming, pause/previous/next/stop, removal, and manual CD return')
    finally:
        app.terminate()
        app.wait(timeout=10)
        listener.close()
        log.seek(0)
        if app.returncode not in (0,-15):
            print(log.read())
        log.close()
