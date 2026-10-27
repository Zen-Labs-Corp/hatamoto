//go:build linux

package supervisor

import (
	"os/exec"
	"syscall"
)

// setProc puts the child in its own process group and has the kernel send it
// SIGTERM if the supervisor dies. Pdeathsig tracks the forking thread, which
// is why StartChild pins one.
func setProc(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
