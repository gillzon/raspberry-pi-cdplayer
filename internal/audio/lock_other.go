//go:build !linux

package audio

import (
	"fmt"
	"os"
)

func lockCache(*os.File) error { return fmt.Errorf("persistent CD cache requires Linux") }
