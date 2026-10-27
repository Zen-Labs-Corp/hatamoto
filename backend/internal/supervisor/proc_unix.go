//go:build unix && !linux

package supervisor

import (
	"os/exec"
	"syscall"
)

// setProc puts the child in its own process group.
func setProc(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
