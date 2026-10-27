//go:build unix

package supervisor

import "syscall"

func termProc(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// killGroup SIGKILLs the child's whole process group (the child is its leader).
func killGroup(pid int) error { return syscall.Kill(-pid, syscall.SIGKILL) }

// hupProc sends SIGHUP, the "look at state.json" request.
func hupProc(pid int) error { return syscall.Kill(pid, syscall.SIGHUP) }
