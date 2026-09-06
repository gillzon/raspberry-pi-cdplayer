# Persistent libcdio reader. Stdout is a binary protocol, stderr diagnostics only.
# libcdio_cdda handles drive-specific reads and converts samples to host endian.
import ctypes as C
import ctypes.util
import json
import faulthandler
import os
import signal
import struct
import sys


def main():
    faulthandler.enable()
    # Do not leave a reader running after an unexpected Go process exit.
    parent = os.getppid()
    if sys.platform.startswith('linux'):
        if C.CDLL(None).prctl(1, signal.SIGTERM, 0, 0, 0) != 0:
            raise RuntimeError('cannot bind reader lifetime to parent')
        if os.getppid() != parent:
            raise RuntimeError('player exited during reader startup')
    libname = ctypes.util.find_library('cdio_cdda')
    if not libname:
        raise RuntimeError('libcdio_cdda missing; install the libcdio-cdda runtime package')
    lib = C.CDLL(libname)
    def fn(name, result, *args):
        f = getattr(lib, name)
        f.restype, f.argtypes = result, list(args)
        return f
    identify = fn('cdio_cddap_identify', C.c_void_p, C.c_char_p, C.c_int, C.c_void_p)
    open_drive = fn('cdio_cddap_open', C.c_int, C.c_void_p)
    close_drive = fn('cdio_cddap_close', C.c_int, C.c_void_p)
    paraname = ctypes.util.find_library('cdio_paranoia')
    if not paraname:
        raise RuntimeError('libcdio_paranoia missing; install the libcdio-paranoia runtime package')
    para_lib = C.CDLL(paraname)
    def para_fn(name, result, *args):
        f = getattr(para_lib, name)
        f.restype, f.argtypes = result, list(args)
        return f
    init = para_fn('cdio_paranoia_init', C.c_void_p, C.c_void_p)
    free = para_fn('cdio_paranoia_free', None, C.c_void_p)
    mode = para_fn('cdio_paranoia_modeset', None, C.c_void_p, C.c_int)
    seek = para_fn('cdio_paranoia_seek', C.c_int32, C.c_void_p, C.c_int32, C.c_int)
    read = para_fn('cdio_paranoia_read_limited', C.c_void_p, C.c_void_p, C.c_void_p, C.c_int)
    first = fn('cdio_cddap_track_firstsector', C.c_int32, C.c_void_p, C.c_ubyte)
    last = fn('cdio_cddap_track_lastsector', C.c_int32, C.c_void_p, C.c_ubyte)
    is_audio = fn('cdio_cddap_track_audiop', C.c_int, C.c_void_p, C.c_ubyte)
    if sys.argv[1] == '--check':
        print('Persistent CD reader dependencies available')
        return
    drive = identify(sys.argv[1].encode(), 1, None)
    if not drive:
        raise RuntimeError('cannot identify CD drive ' + sys.argv[1])
    para = None
    try:
        if open_drive(drive) != 0:
            raise RuntimeError('cannot open CD drive ' + sys.argv[1])
        layout = json.loads(sys.argv[2])
        for t in layout:
            n = t['number']
            actual_start, actual_end = first(drive, n), last(drive, n) + 1
            if not is_audio(drive, n) or actual_start != t['start'] or actual_end != t['end']:
                raise RuntimeError(f"track {n} layout mismatch: expected {t['start']}..{t['end']}, reader reported {actual_start}..{actual_end}")
        para = init(drive)
        if not para:
            raise RuntimeError('cannot initialize CD reader')
        mode(para, 0xff ^ 0x20)
        next_sector = None
        sys.stdout.buffer.write(b'CDPCM1\n')
        sys.stdout.buffer.flush()
        for line in sys.stdin.buffer:
            sector, count = map(int, line.split())
            if count < 1 or count > 75 or not any(t['start'] <= sector and sector + count <= t['end'] for t in layout):
                raise RuntimeError('read outside audio track')
            if next_sector != sector:
                if seek(para, sector, 0) < 0:
                    raise RuntimeError('CD seek failed')
            blocks = []
            for _ in range(count):
                block = read(para, None, 20)
                if not block:
                    raise RuntimeError('CD read failed at sector ' + str(sector))
                blocks.append(C.string_at(block, 2352))
            data = b''.join(blocks)
            next_sector = sector + count
            if sys.byteorder != 'little':
                swapped = bytearray(data)
                swapped[0::2], swapped[1::2] = data[1::2], data[0::2]
                data = bytes(swapped)
            sys.stdout.buffer.write(struct.pack('<I', len(data)) + data)
            sys.stdout.buffer.flush()
    finally:
        if para:
            free(para)
        close_drive(drive)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('CD reader: ' + str(error), file=sys.stderr)
        sys.exit(1)
