//go:build linux && (amd64 || arm64 || arm || 386)

package disc

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

// Drive uses the Linux CD-ROM ABI, without cgo or a mounted filesystem.
type Drive struct{ Device string }

func (d Drive) Read() (Disc, error) {
	fd, err := syscall.Open(d.Device, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.ENOMEDIUM) {
			return Disc{}, nil
		}
		return Disc{}, fmt.Errorf("open %s: %w", d.Device, err)
	}
	defer syscall.Close(fd)
	status, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x5326, 0x7fffffff) // CDROM_DRIVE_STATUS, CDSL_CURRENT
	if errno != 0 {
		return Disc{}, fmt.Errorf("drive status: %w", errno)
	}
	switch status {
	case 1, 2: // CDS_NO_DISC, CDS_TRAY_OPEN
		return Disc{}, nil
	case 4: // CDS_DISC_OK
	default:
		return Disc{}, fmt.Errorf("drive not ready (status %d)", status)
	}
	var header [2]byte
	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x5305, uintptr(unsafe.Pointer(&header)))
	if errno != 0 {
		return Disc{}, fmt.Errorf("read CD table of contents: %w", errno)
	}
	if header[0] < 1 || header[1] > 99 || header[0] > header[1] {
		return Disc{}, fmt.Errorf("invalid CD track range %d–%d", header[0], header[1])
	}
	var tracks []int
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
		if number != 0xaa && (b[1]>>4)&4 == 0 { // CDROM_DATA_TRACK
			tracks = append(tracks, track)
		}
	}
	return Disc{ID: fmt.Sprintf("%x", hash.Sum(nil)), Tracks: tracks}, nil
}
