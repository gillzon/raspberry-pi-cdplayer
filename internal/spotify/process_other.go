//go:build !linux

package spotify

import "os/exec"

func protectChild(cmd *exec.Cmd) {}
