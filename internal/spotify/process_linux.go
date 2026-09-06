package spotify

import (
	"os/exec"
	"syscall"
)

func protectChild(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} }
