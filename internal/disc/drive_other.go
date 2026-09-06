//go:build !linux || (!amd64 && !arm64 && !arm && !386)

package disc

import "fmt"

type Drive struct{ Device string }

func (d Drive) Eject() error { return fmt.Errorf("disc eject requires supported Linux hardware") }

func (d Drive) Read() (Disc, error) {
	return Disc{}, fmt.Errorf("CD detection requires Linux on amd64, arm64, arm, or 386")
}
