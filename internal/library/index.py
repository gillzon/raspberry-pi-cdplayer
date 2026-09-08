"""Embedded USB index helper: Python's SQLite plus Mutagen, no writes to USB."""
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import sys
import unicodedata

MAX_ART = 5 * 1024 * 1024


def normalize(value):
    return ''.join(c for c in unicodedata.normalize('NFKD', value.casefold())
                   if not unicodedata.combining(c))


def connect(path):
    db = sqlite3.connect(path, timeout=10)
    db.row_factory = sqlite3.Row
    db.execute('PRAGMA journal_mode=WAL')
    db.executescript('''
        CREATE TABLE IF NOT EXISTS tracks (
            id TEXT PRIMARY KEY, path TEXT UNIQUE, title TEXT, artist TEXT,
            album TEXT, track INTEGER, duration REAL, cover TEXT, search TEXT,
            stamp TEXT);
        CREATE TABLE IF NOT EXISTS artwork (id TEXT PRIMARY KEY, data BLOB);
    ''')
    return db


def image(data):
    return len(data) <= MAX_ART and (data.startswith(b'\xff\xd8\xff') or
                                   data.startswith(b'\x89PNG\r\n\x1a\n'))


def folder_cover(directory):
    files = {p.name.lower(): p for p in directory.iterdir() if not p.is_symlink()}
    for name in ('cover.jpg', 'cover.jpeg', 'cover.png', 'folder.jpg', 'folder.jpeg', 'folder.png'):
        p = files.get(name)
        if p and p.is_file() and p.stat().st_size <= MAX_ART:
            data = p.read_bytes()
            if image(data):
                return data
    return b''


def scan(db, root):
    from mutagen import MutagenError
    from mutagen.mp3 import MP3
    from mutagen.id3 import ID3
    root = Path(root).resolve(strict=True)
    if not root.is_dir():
        raise ValueError('USB music location is not a directory')
    old = {r['path']: r['stamp'] for r in db.execute('SELECT path, stamp FROM tracks')}
    seen = []
    warnings = 0
    def walk_error(error):
        raise error  # Roll back an incomplete scan; do not prune unreadable folders.
    with db:
        for directory, dirs, files in os.walk(root, onerror=walk_error, followlinks=False):
            dirs[:] = sorted(d for d in dirs if not (Path(directory) / d).is_symlink())
            directory = Path(directory)
            cover = folder_cover(directory)
            cover_hash = hashlib.sha256(cover).hexdigest() if cover else ''
            for name in sorted(files):
                p = directory / name
                if p.suffix.lower() != '.mp3' or p.is_symlink() or not p.is_file():
                    continue
                rel = str(p.relative_to(root))
                stat = p.stat()
                stamp = f'{stat.st_mtime_ns}:{stat.st_size}:{cover_hash}'
                seen.append((rel,))
                if old.get(rel) == stamp:
                    continue
                tags, duration = {}, 0
                try:
                    audio = MP3(p)
                    tags, duration = audio.tags or {}, audio.info.length
                except MutagenError:
                    warnings += 1
                    try:
                        tags = ID3(p)
                    except MutagenError:
                        pass
                def tag(key, fallback=''):
                    value = str(tags.get(key, '')).strip()
                    return value or fallback
                title = tag('TIT2', p.stem)
                artist = tag('TPE1', tag('TPE2', 'Unknown artist'))
                album = tag('TALB', directory.name)
                try:
                    track = max(0, int(tag('TRCK', '0').split('/')[0]))
                except ValueError:
                    track = 0
                art = cover
                if hasattr(tags, 'getall'):
                    pictures = sorted(tags.getall('APIC'), key=lambda a: a.type != 3)
                    for picture in pictures:
                        if image(picture.data):
                            art = picture.data
                            break
                art_id = hashlib.sha256(art).hexdigest() if art else ''
                if art:
                    db.execute('INSERT OR IGNORE INTO artwork VALUES (?, ?)', (art_id, art))
                ident = hashlib.sha256(rel.encode()).hexdigest()
                db.execute('INSERT OR REPLACE INTO tracks VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
                           (ident, rel, title, artist, album, track, duration, art_id,
                            normalize(' '.join((title, artist, album))), stamp))
        db.execute('DROP TABLE IF EXISTS temp.seen')
        db.execute('CREATE TEMP TABLE seen (path TEXT PRIMARY KEY)')
        db.executemany('INSERT INTO seen VALUES (?)', seen)
        db.execute('DELETE FROM tracks WHERE path NOT IN (SELECT path FROM seen)')
        db.execute('DELETE FROM artwork WHERE id NOT IN (SELECT cover FROM tracks)')
    return {'count': len(seen), 'warnings': warnings}


def public(row):
    result = dict(row)
    result.pop('search', None)
    result.pop('stamp', None)
    cover = result.pop('cover')
    result['cover_url'] = '/api/library/art/' + cover if cover else ''
    return result


def main():
    command, database, root, *args = sys.argv[1:]
    db = connect(database)
    if command == 'scan':
        result = scan(db, root)
    elif command == 'search':
        terms = normalize(args[0]).split()[:20]
        where = ' AND '.join('instr(search, ?) > 0' for _ in terms) or '1'
        total = db.execute('SELECT count(*) FROM tracks WHERE ' + where, terms).fetchone()[0]
        rows = db.execute('SELECT * FROM tracks WHERE ' + where +
                          ' ORDER BY artist COLLATE NOCASE, album COLLATE NOCASE, track, title, id LIMIT 20 OFFSET ?',
                          [*terms, int(args[1])])
        result = {'tracks': [public(r) for r in rows], 'total': total}
        for track in result['tracks']:
            track.pop('path')
    elif command == 'mix':
        result = [public(row) for row in db.execute('SELECT * FROM tracks ORDER BY RANDOM()')]
    elif command == 'get':
        row = db.execute('SELECT * FROM tracks WHERE id = ?', (args[0],)).fetchone()
        if row is None:
            raise ValueError('Song is no longer indexed; refresh the library')
        result = public(row)
    elif command == 'album':
        row = db.execute('SELECT * FROM tracks WHERE id = ?', (args[0],)).fetchone()
        if row is None:
            raise ValueError('Song is no longer indexed; refresh the library')
        rows = db.execute('SELECT * FROM tracks WHERE album = ? ORDER BY track, title, id', (row['album'],))
        result = [public(r) for r in rows if Path(r['path']).parent == Path(row['path']).parent]
    elif command == 'art':
        row = db.execute('SELECT data FROM artwork WHERE id = ?', (args[0],)).fetchone()
        if row:
            sys.stdout.buffer.write(row[0])
        return
    else:
        raise ValueError('Unknown library operation')
    print(json.dumps(result))


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
