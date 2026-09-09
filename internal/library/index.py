"""Embedded USB index helper: Python's SQLite plus Mutagen, no writes to USB."""
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import sys
import time
import unicodedata

MAX_ART = 5 * 1024 * 1024
CHECKPOINT_FILES = 100
CHECKPOINT_SECONDS = 5


def normalize(value):
    return ''.join(c for c in unicodedata.normalize('NFKD', value.casefold())
                   if not unicodedata.combining(c))


def connect(path):
    db = sqlite3.connect(path, timeout=10)
    db.row_factory = sqlite3.Row
    db.execute('PRAGMA journal_mode=WAL')
    db.execute('PRAGMA synchronous=FULL')
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


def index_track(db, root, p, cover, cover_hash):
    from mutagen import MutagenError
    from mutagen.mp3 import MP3
    from mutagen.id3 import ID3
    rel = str(p.relative_to(root))
    stat = p.stat()
    stamp = f'{stat.st_mtime_ns}:{stat.st_size}:{cover_hash}'
    old = db.execute('SELECT stamp FROM tracks WHERE path = ?', (rel,)).fetchone()
    if old and old['stamp'] == stamp:
        return 0, False
    warnings = 0
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
    album = tag('TALB', p.parent.name)
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
    return warnings, old is None


def scan(db, root, report=None):
    root = Path(root).resolve(strict=True)
    if not root.is_dir():
        raise ValueError('USB music location is not a directory')
    identity = root.stat()
    count = db.execute('SELECT count(*) FROM tracks').fetchone()[0]
    total = processed = warnings = checkpointed = 0
    last_save = last_report = time.monotonic()

    def progress(phase):
        if report:
            report({'phase': phase, 'count': count, 'warnings': warnings,
                    'total': total, 'processed': processed, 'checkpointed': checkpointed,
                    'percent': 100 if phase == 'complete' else min(99, processed * 100 // total) if total else 0})

    def walk_error(error):
        raise error  # Never prune after an unreadable/incomplete directory walk.

    # Count paths without reading tags/artwork. The temporary SQLite table can
    # spill to disk, rather than retaining a whole large library in Python RAM.
    db.execute('DROP TABLE IF EXISTS temp.scan_files')
    db.execute('CREATE TEMP TABLE scan_files (path TEXT PRIMARY KEY)')
    progress('discovering')
    with db:
        for directory, dirs, files in os.walk(root, onerror=walk_error, followlinks=False):
            dirs[:] = sorted(d for d in dirs if not (Path(directory) / d).is_symlink())
            for name in sorted(files):
                p = Path(directory) / name
                if p.suffix.lower() != '.mp3' or p.is_symlink() or not p.is_file():
                    continue
                db.execute('INSERT INTO scan_files VALUES (?)', (str(p.relative_to(root)),))
                total += 1
                if time.monotonic() - last_report >= 1:
                    progress('discovering')
                    last_report = time.monotonic()
    progress('indexing')
    previous_directory = None
    with db:
        for row in db.execute('SELECT path FROM scan_files ORDER BY path'):
            p = root / row['path']
            if p.is_symlink() or not p.is_file() or not p.resolve(strict=True).is_relative_to(root):
                raise OSError('USB file moved or disappeared during scan: ' + row['path'])
            if p.parent != previous_directory:
                cover = folder_cover(p.parent)
                cover_hash = hashlib.sha256(cover).hexdigest() if cover else ''
                previous_directory = p.parent
            file_warnings, added = index_track(db, root, p, cover, cover_hash)
            warnings += file_warnings
            count += int(added)
            processed += 1
            # Commit before publishing progress: these tracks and their artwork
            # are searchable now and survive restart. Only this batch rolls back.
            if processed - checkpointed >= CHECKPOINT_FILES or time.monotonic() - last_save >= CHECKPOINT_SECONDS:
                db.commit()
                checkpointed = processed
                last_save = time.monotonic()
                progress('indexing')
        db.commit()
        checkpointed = processed
        progress('finalizing')
        current = root.stat()
        if (current.st_dev, current.st_ino) != (identity.st_dev, identity.st_ino):
            raise OSError('USB music location changed during scan; keeping saved songs')
        # Cleanup is one final atomic transaction, only after a successful scan.
        db.execute('DELETE FROM tracks WHERE path NOT IN (SELECT path FROM scan_files)')
        db.execute('DELETE FROM artwork WHERE id NOT IN (SELECT cover FROM tracks)')
    count = total
    progress('complete')
    return {'count': count, 'warnings': warnings, 'phase': 'complete', 'total': total,
            'processed': processed, 'checkpointed': checkpointed, 'percent': 100}


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
    if command == 'scan-progress':
        scan(db, root, lambda value: print(json.dumps(value), flush=True))
        return
    elif command == 'scan':
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
