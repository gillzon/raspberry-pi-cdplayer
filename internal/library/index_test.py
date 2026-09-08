import importlib.util
import json
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from mutagen.id3 import ID3, TIT2, TPE1, TALB, TRCK, APIC

spec = importlib.util.spec_from_file_location('music_index', Path(__file__).with_name('index.py'))
index = importlib.util.module_from_spec(spec)
spec.loader.exec_module(index)
PNG = b'\x89PNG\r\n\x1a\nfixture'
JPEG = b'\xff\xd8\xfffixture'


class LibraryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'usb'
        self.root.mkdir()
        self.dbpath = Path(self.temp.name) / 'library.sqlite'
        self.db = index.connect(self.dbpath)
        self.addCleanup(self.db.close)

    def song(self, name, title='Song', artist='Björk', album='Debut', track=1, art=None):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        tags = ID3()
        for frame in (TIT2(text=title), TPE1(text=artist), TALB(text=album), TRCK(text=str(track))):
            tags.add(frame)
        if art:
            tags.add(APIC(mime='image/png', type=3, data=art))
        tags.save(path)
        return path

    def command(self, action, *args):
        return subprocess.check_output([sys.executable, str(Path(index.__file__)), action,
                                        str(self.dbpath), str(self.root), *args])

    def test_search_across_fields_unicode_literal_sql_and_pagination(self):
        self.song('one.MP3', title='Human Behaviour')
        self.song('two.mp3', title='100% Love', artist='Other')
        index.scan(self.db, self.root)
        result = json.loads(self.command('search', 'BJORK debut human', '0'))
        self.assertEqual(result['total'], 1)
        self.assertEqual(result['tracks'][0]['title'], 'Human Behaviour')
        self.assertNotIn('path', result['tracks'][0])
        self.assertEqual(json.loads(self.command('search', '%', '0'))['total'], 1)
        self.assertEqual(json.loads(self.command('search', "' OR 1=1 --", '0'))['total'], 0)
        self.assertEqual(len(json.loads(self.command('search', '', '1'))['tracks']), 1)

    def test_twenty_per_page_and_mix_includes_entire_library(self):
        for number in range(45):
            self.song(f'{number}.mp3', title=f'Song {number:02}')
        index.scan(self.db, self.root)
        pages = [json.loads(self.command('search', '', str(offset))) for offset in (0, 20, 40)]
        self.assertEqual([len(p['tracks']) for p in pages], [20, 20, 5])
        self.assertTrue(all(p['total'] == 45 for p in pages))
        ids = [t['id'] for p in pages for t in p['tracks']]
        self.assertEqual(len(set(ids)), 45)
        mix = json.loads(self.command('mix'))
        self.assertEqual(len(mix), 45)
        self.assertEqual({t['id'] for t in mix}, set(ids))
        self.assertEqual(json.loads(self.command('search', '', '60'))['tracks'], [])

    def test_art_precedence_dedup_changes_and_album_order(self):
        first = self.song('album/one.mp3', track=2, art=PNG)
        self.song('album/two.mp3', track=1, art=PNG)
        self.song('other/three.mp3', track=3)
        cover = first.parent / 'Cover.JPG'
        cover.write_bytes(JPEG)
        index.scan(self.db, self.root)
        rows = list(self.db.execute('SELECT * FROM tracks WHERE path LIKE "album/%" ORDER BY track'))
        self.assertEqual(len(set(r['cover'] for r in rows)), 1)
        self.assertEqual(self.command('art', rows[0]['cover']), PNG)
        album = json.loads(self.command('album', rows[0]['id']))
        self.assertEqual([t['track'] for t in album], [1, 2])
        self.song('album/one.mp3', title='New title')
        index.scan(self.db, self.root)
        updated = self.db.execute('SELECT * FROM tracks WHERE path = ?', ('album/one.mp3',)).fetchone()
        self.assertEqual(updated['title'], 'New title')
        self.assertEqual(self.command('art', updated['cover']), JPEG)
        cover.write_bytes(PNG)
        index.scan(self.db, self.root)
        updated = self.db.execute('SELECT * FROM tracks WHERE path = ?', ('album/one.mp3',)).fetchone()
        self.assertEqual(self.command('art', updated['cover']), PNG)

    def test_prune_unplug_rollback_and_skip_symlinks(self):
        song = self.song('one.mp3')
        (self.root / 'linked.mp3').symlink_to(song)
        (self.root / 'loop').symlink_to(self.root, target_is_directory=True)
        self.assertEqual(index.scan(self.db, self.root)['count'], 1)
        moved = self.root.with_name('unplugged')
        self.root.rename(moved)
        with self.assertRaises(FileNotFoundError):
            index.scan(self.db, self.root)
        self.assertEqual(self.db.execute('SELECT count(*) FROM tracks').fetchone()[0], 1)
        moved.rename(self.root)
        with patch.object(index, 'folder_cover', side_effect=PermissionError('unreadable')):
            with self.assertRaises(PermissionError):
                index.scan(self.db, self.root)
        self.assertEqual(self.db.execute('SELECT count(*) FROM tracks').fetchone()[0], 1)
        song.unlink()
        index.scan(self.db, self.root)
        self.assertEqual(self.db.execute('SELECT count(*) FROM tracks').fetchone()[0], 0)

    def test_filename_fallback_and_incremental_scan(self):
        (self.root / 'Untitled.mp3').write_bytes(b'bad mp3')
        index.scan(self.db, self.root)
        row = self.db.execute('SELECT * FROM tracks').fetchone()
        self.assertEqual(row['title'], 'Untitled')
        self.assertEqual(row['artist'], 'Unknown artist')
        with patch('mutagen.mp3.MP3', side_effect=AssertionError('unchanged file reread')):
            index.scan(self.db, self.root)


if __name__ == '__main__':
    unittest.main()
