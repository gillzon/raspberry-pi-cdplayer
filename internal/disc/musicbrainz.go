package disc

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"strings"
)

// musicBrainzID uses absolute CD frame offsets (MSF already includes the
// 150-frame lead-in). Slot zero is lead-out; unused track slots remain zero.
func musicBrainzID(first, last int, offsets [100]uint32) string {
	var text strings.Builder
	fmt.Fprintf(&text, "%02X%02X", first, last)
	for _, offset := range offsets {
		fmt.Fprintf(&text, "%08X", offset)
	}
	hash := sha1.Sum([]byte(text.String()))
	return base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._").WithPadding('-').EncodeToString(hash[:])
}
