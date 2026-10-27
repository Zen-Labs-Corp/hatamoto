//go:build !unix

package supervisor

import (
	"errors"
	"os/exec"
)

var errUnsupported = errors.New("supervisor: unsupported platform")

func setProc(c *exec.Cmd)    {}
func termProc(pid int) error { return errUnsupported }
func killGroup(int) error    { return errUnsupported }
func hupProc(int) error      { return errUnsupported }
