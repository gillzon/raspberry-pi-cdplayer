package disc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// The system utility handles unlocking and alternative eject methods used by
// USB optical drives. A single CDROMEJECT ioctl does not cover those cases.
func ejectDevice(ctx context.Context, device string) error {
	output, err := exec.CommandContext(ctx, "eject", "--verbose", "--", device).CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("eject %s: %w", device, ctx.Err())
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("eject command is missing; install it with sudo apt install eject")
	}
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("eject %s: %w: %s", device, err, message)
		}
		return fmt.Errorf("eject %s: %w", device, err)
	}
	return nil
}
