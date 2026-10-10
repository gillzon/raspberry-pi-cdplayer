# Persistent libcdio reader. Stdout is a binary protocol, stderr diagnostics only.
# libcdio_cdda handles drive-specific reads and converts samples to host endian.
import ctypes as C
import ctypes.util
import json
import faulthandler
import os
import stat
import struct
import sys
import threading
import time


def watch_parent(parent):
    # PR_SET_PDEATHSIG follows the spawning Linux thread, which may exit
    # while a Go process is still healthy. Watch the process relationship.
    def watch():
        while os.getppid() == parent:
            threading.Event().wait(0.5)
        print('CD reader: player process exited', file=sys.stderr, flush=True)
        os._exit(1)
    if os.getppid() != parent:
        raise RuntimeError('player exited during reader startup')
    threading.Thread(target=watch, daemon=True).start()


def linux_speed(device, speed):
    """Fallback for physical Linux drives; never issue ioctls on image files."""
    if not sys.platform.startswith('linux') or not stat.S_ISBLK(os.stat(device).st_mode):
        raise OSError('not a Linux block device')
    import fcntl
    fd = os.open(device, os.O_RDONLY | os.O_NONBLOCK | os.O_CLOEXEC)
    try:
        fcntl.ioctl(fd, 0x5322, speed)  # CDROM_SELECT_SPEED, multiplier (not a pointer)
    finally:
        os.close(fd)


def request_speed(set_speed, handle, speed, phase):
    result = set_speed(handle, speed)
    if result != 0:
        try:
            linux_speed(sys.argv[1], speed)
        except OSError as error:
            print(f'CD reader: WARNING: cannot enforce {speed}x {phase} speed (libcdio {result}; Linux fallback: {error}); drive may remain noisy', file=sys.stderr, flush=True)
            return False
        print(f'CD reader: Linux fallback accepted {speed}x {phase} speed request; actual speed is not measured', file=sys.stderr, flush=True)
        return True
    print(f'CD reader: drive accepted {speed}x {phase} speed request; actual speed is not measured', file=sys.stderr, flush=True)
    return True


class ReadPacer:
    """Limit sustained extraction even when firmware ignores the speed request.

    No accumulated credit: a long seek or pause must not cause a catch-up burst.
    The first read starts immediately; subsequent reads allow playback headroom.
    This limits data throughput, not the motor's physical speed.
    """
    def __init__(self, speed):
        self.speed = speed
        self.next_read = 0

    def begin(self):
        if self.speed > 0:
            delay = self.next_read - time.monotonic()
            if delay > 0:
                time.sleep(delay)
        return time.monotonic()

    def finish(self, started, sectors):
        if self.speed > 0:
            self.next_read = started + sectors / (75 * self.speed)


def main():
    faulthandler.enable()
    # Do not leave a reader running after an unexpected Go process exit.
    if sys.argv[1] != '--check':
        watch_parent(int(sys.argv[3]))
    libname = ctypes.util.find_library('cdio_cdda')
    if not libname:
        raise RuntimeError('libcdio_cdda missing; install the libcdio-cdda runtime package')
    lib = C.CDLL(libname)
    def fn(name, result, *args):
        f = getattr(lib, name)
        f.restype, f.argtypes = result, list(args)
        return f
    identify = fn('cdio_cddap_identify_cdio', C.c_void_p, C.c_void_p, C.c_int, C.c_void_p)
    open_drive = fn('cdio_cddap_open', C.c_int, C.c_void_p)
    close_drive = fn('cdio_cddap_close', C.c_int, C.c_void_p)
    set_speed = fn('cdio_cddap_speed_set', C.c_int, C.c_void_p, C.c_int)
    read_audio = fn('cdio_cddap_read', C.c_long, C.c_void_p, C.c_void_p, C.c_int32, C.c_long)
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
    cdioname = ctypes.util.find_library('cdio')
    if not cdioname:
        raise RuntimeError('libcdio missing; install the libcdio runtime package')
    cdio = C.CDLL(cdioname)
    def cdio_fn(name, result, *args):
        f = getattr(cdio, name)
        f.restype, f.argtypes = result, list(args)
        return f
    open_cdio = cdio_fn('cdio_open', C.c_void_p, C.c_char_p, C.c_int)
    close_cdio = cdio_fn('cdio_destroy', None, C.c_void_p)
    set_cdio_speed = cdio_fn('cdio_set_speed', C.c_int, C.c_void_p, C.c_int)
    if sys.argv[1] == '--check':
        print('Persistent CD reader dependencies available')
        return
    handle = open_cdio(sys.argv[1].encode(), 0)  # DRIVER_UNKNOWN
    if not handle:
        raise RuntimeError('cannot open CD device ' + sys.argv[1])
    speed = globals().get('CD_READ_SPEED', 0)
    startup_speed = globals().get('CD_STARTUP_SPEED', 0)
    boosted = speed > 0 and startup_speed > speed
    quiet_pending = boosted
    drive = None
    para = None
    try:
        if boosted:
            # Set this before CDDA initialization, which performs probe reads
            # and may otherwise inherit the previous disc's quiet speed.
            request_speed(set_cdio_speed, handle, startup_speed, 'startup')
        elif speed > 0:
            # A disabled boost must also keep CDDA's probe reads quiet.
            request_speed(set_cdio_speed, handle, speed, 'playback')
        drive = identify(handle, 1, None)
        if not drive:
            raise RuntimeError('cannot identify CD drive ' + sys.argv[1])
        if open_drive(drive) != 0:
            raise RuntimeError('cannot open CD drive ' + sys.argv[1])
        if speed > 0 and not boosted:
            request_speed(set_speed, drive, speed, 'playback')
        layout = json.loads(sys.argv[2])
        for t in layout:
            n = t['number']
            actual_start, actual_end = first(drive, n), last(drive, n) + 1
            if not is_audio(drive, n) or actual_start != t['start'] or actual_end != t['end']:
                raise RuntimeError(f"track {n} layout mismatch: expected {t['start']}..{t['end']}, reader reported {actual_start}..{actual_end}")
        if globals().get('VERIFY_AUDIO', False):
            para = init(drive)
            if not para:
                raise RuntimeError('cannot initialize CD reader')
            mode(para, 0xff ^ 0x20)
        next_sector = None
        pacer = ReadPacer(speed)
        if speed > 0:
            print(f'CD reader: background extraction paced at at most {speed}x audio rate', file=sys.stderr, flush=True)
        sys.stdout.buffer.write(b'CDPCM1\n')
        sys.stdout.buffer.flush()
        for line in sys.stdin.buffer:
            sector, count = map(int, line.split())
            if count < 1 or count > 75 or not any(t['start'] <= sector and sector + count <= t['end'] for t in layout):
                raise RuntimeError('read outside audio track')
            started = pacer.begin()
            if para:
                if next_sector != sector:
                    if seek(para, sector, 0) < 0:
                        raise RuntimeError('CD seek failed')
                blocks = []
                for offset in range(count):
                    block = read(para, None, 20)
                    if not block:
                        raise RuntimeError('CD read failed at sector ' + str(sector + offset))
                    blocks.append(C.string_at(block, 2352))
                data = b''.join(blocks)
                next_sector = sector + count
            else:
                # The standard CDDA API handles drive transfer limits and
                # native sample byte order. A short read is completed by Go;
                # never pad unread sectors with silence.
                buffer = C.create_string_buffer(count * 2352)
                actual = read_audio(drive, buffer, sector, count)
                if actual <= 0 or actual > count:
                    raise RuntimeError(f'CD read failed at sector {sector} (result {actual})')
                data = buffer.raw[:actual * 2352]
            if sys.byteorder != 'little':
                swapped = bytearray(data)
                swapped[0::2], swapped[1::2] = data[1::2], data[0::2]
                data = bytes(swapped)
            if quiet_pending:
                # MPD cannot start cached playback until this first response.
                # Subsequent blocks and track changes keep the quiet speed.
                request_speed(set_speed, drive, speed, 'playback')
                quiet_pending = False
            pacer.finish(started, len(data) // 2352)
            sys.stdout.buffer.write(struct.pack('<I', len(data)) + data)
            sys.stdout.buffer.flush()
    finally:
        if quiet_pending:
            # Restore quiet speed on initialization/read failure or clean EOF.
            request_speed(set_cdio_speed, handle, speed, 'playback')
        if para:
            free(para)
        if drive:
            close_drive(drive)  # Also releases the owned CdIo handle.
        else:
            close_cdio(handle)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('CD reader: ' + str(error), file=sys.stderr)
        sys.exit(1)
