//go:build linux && (amd64 || arm64 || arm || 386)

package disc

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"
)

// Drive uses the Linux CD-ROM ABI, without cgo or a mounted filesystem.
type Drive struct {
	Device string
	cache  tocCache
}

// Eject opens the configured tray after MPD has released its audio reader.
func (d *Drive) Eject() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ejectDevice(ctx, d.Device); err != nil {
		return err
	}
	d.cache = tocCache{}
	return nil
}

func (d *Drive) Read() (Disc, error) {
	fd, err := syscall.Open(d.Device, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.ENOMEDIUM) {
			d.cache = tocCache{}
			return Disc{}, nil
		}
		return Disc{}, fmt.Errorf("open %s: %w", d.Device, err)
	}
	defer syscall.Close(fd)
	status, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x5326, 0x7fffffff) // CDROM_DRIVE_STATUS, CDSL_CURRENT
	if errno != 0 {
		d.cache = tocCache{}
		return Disc{}, fmt.Errorf("drive status: %w", errno)
	}
	return d.cache.read(int(status), func() (Disc, error) { return readTOC(fd) })
}

func readTOC(fd int) (Disc, error) {
	var header [2]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x5305, uintptr(unsafe.Pointer(&header)))
	if errno != 0 {
		return Disc{}, fmt.Errorf("read CD table of contents: %w", errno)
	}
	if header[0] < 1 || header[1] > 99 || header[0] > header[1] {
		return Disc{}, fmt.Errorf("invalid CD track range %d–%d", header[0], header[1])
	}
	var tracks []int
	var offsets [100]uint32
	hash := sha256.New()
	for track := int(header[0]); track <= int(header[1])+1; track++ {
		number := byte(track)
		if track == int(header[1])+1 {
			number = 0xaa // lead-out identifies duration, including single-track discs
		}
		// struct cdrom_tocentry: byte track, bitfields adr/ctrl, byte format,
		// one padding byte, 4-byte address union, byte data mode, padding.
		// The supported architectures use little-endian bitfield ordering.
		var entry [3]uint32 // 12 bytes, aligned for the kernel ABI
		b := (*[12]byte)(unsafe.Pointer(&entry))
		b[0], b[2] = number, 2 // CDROM_MSF
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x5306, uintptr(unsafe.Pointer(&entry)))
		if errno != 0 {
			return Disc{}, fmt.Errorf("read TOC track %d: %w", number, errno)
		}
		hash.Write(b[:])
		index := track
		if number == 0xaa {
			index = 0
		}
		offsets[index] = (uint32(b[4])*60+uint32(b[5]))*75 + uint32(b[6])
		if number != 0xaa && (b[1]>>4)&4 == 0 { // CDROM_DATA_TRACK
			tracks = append(tracks, track)
		}
	}
	d := Disc{ID: fmt.Sprintf("%x", hash.Sum(nil)), Tracks: tracks}
	for _, number := range tracks {
		end := offsets[0]
		if number != int(header[1]) {
			end = offsets[number+1]
		}
		d.Layout = append(d.Layout, TrackLayout{Number: number, Start: int(offsets[number]) - 150, End: int(end) - 150})
	}
	// Mixed-session layouts need session-specific lead-out handling. Avoid
	// sending an incorrect identifier for those discs; playback still works.
	if len(tracks) == int(header[1]-header[0])+1 {
		d.MusicBrainzID = musicBrainzID(int(header[0]), int(header[1]), offsets)
	}
	return d, nil
}
