package disc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AutoDrive follows the single optical drive reported by Linux, including
// changes from sr0 to sr1 after a USB reconnection. No filesystem mount is needed.
type AutoDrive struct {
	drive    Drive
	OnChange func(string)
	detect   func() (string, error)
}

func (d *AutoDrive) DevicePath() string { return d.drive.Device }
func (d *AutoDrive) Read() (Disc, error) {
	detect := d.detect
	if detect == nil {
		detect = func() (string, error) { return findOpticalDrive("/sys/class/block", "/dev") }
	}
	path, err := detect()
	if err != nil {
		return Disc{}, err
	}
	previous := d.drive.Device
	if path != previous {
		d.drive = Drive{Device: path}
		if d.OnChange != nil {
			d.OnChange(path)
		}
		// Clear the old queue even if the replacement contains the same album.
		if previous != "" {
			return Disc{}, nil
		}
	}
	if path == "" {
		return Disc{}, nil
	}
	return d.drive.Read()
}
func (d *AutoDrive) Eject() error {
	// Source selection may have suspended disc polling while Spotify was active.
	detect := d.detect
	if detect == nil {
		detect = func() (string, error) { return findOpticalDrive("/sys/class/block", "/dev") }
	}
	path, err := detect()
	if err != nil {
		return err
	}
	if path != d.drive.Device {
		d.drive = Drive{Device: path}
		if d.OnChange != nil {
			d.OnChange(path)
		}
	}
	if d.drive.Device == "" {
		return fmt.Errorf("no CD drive connected")
	}
	return d.drive.Eject()
}
func findOpticalDrive(sysRoot, devRoot string) (string, error) {
	entries, err := os.ReadDir(sysRoot)
	if err != nil {
		return "", fmt.Errorf("detect CD drives: %w", err)
	}
	var found []string
	for _, entry := range entries {
		kind, err := os.ReadFile(filepath.Join(sysRoot, entry.Name(), "device", "type"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect %s: %w", entry.Name(), err)
		}
		if strings.TrimSpace(string(kind)) != "5" {
			continue
		} // SCSI peripheral type CD/DVD
		path := filepath.Join(devRoot, entry.Name())
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return "", err
		}
		found = append(found, path)
	}
	if len(found) > 1 {
		return "", fmt.Errorf("multiple CD drives detected (%s); select one with -device /dev/srN", strings.Join(found, ", "))
	}
	if len(found) == 0 {
		return "", nil
	}
	return found[0], nil
}
