//go:build linux && (amd64 || arm64 || arm || 386)

package disc

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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
	lastSession := 0
	if len(tracks) > 0 && len(tracks) < int(header[1]-header[0])+1 {
		// CD Extra's next data track starts after a session gap, not at the
		// audio lead-out. Query the session address in LBA (already minus 150).
		var session [2]uint32 // struct cdrom_multisession, 8 bytes and aligned
		b := (*[8]byte)(unsafe.Pointer(&session))
		b[5] = 1 // CDROM_LBA
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x5310, uintptr(unsafe.Pointer(&session)))
		if errno == 0 && b[4] != 0 { // xa_flag: session address is valid
			lastSession = int(int32(binary.LittleEndian.Uint32(b[:4])))
		}
	}
	d.Layout = audioLayout(tracks, int(header[0]), int(header[1]), offsets, lastSession)
	// Mixed-session layouts need session-specific lead-out handling. Avoid
	// sending an incorrect identifier for those discs; playback still works.
	if len(tracks) == int(header[1]-header[0])+1 {
		d.MusicBrainzID = musicBrainzID(int(header[0]), int(header[1]), offsets)
	}
	return d, nil
}

func audioLayout(tracks []int, first, last int, offsets [100]uint32, lastSession int) []TrackLayout {
	var audio [100]bool
	for _, number := range tracks {
		audio[number] = true
	}
	var layout []TrackLayout
	for _, number := range tracks {
		start, end := int(offsets[number])-150, int(offsets[0])-150
		if number < last {
			end = int(offsets[number+1]) - 150
			// Match libcdio-paranoia's CD Extra lead-out correction: 90s
			// lead-out + 60s lead-in + 2s pregap. Never trim a single-session
			// mixed-mode disc, or a boundary between two audio tracks.
			const sessionGap = (90 + 60 + 2) * 75
			audioEnd := lastSession - sessionGap
			if !audio[number+1] && lastSession > int(offsets[first])-150 && audioEnd > start && audioEnd < end {
				end = audioEnd
			}
		}
		layout = append(layout, TrackLayout{Number: number, Start: start, End: end})
	}
	return layout
}
