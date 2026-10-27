//go:build unix

package updater

import (
	"fmt"
	"syscall"
)

// FreeDisk returns the bytes an unprivileged process can still write under dir.
func FreeDisk(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", dir, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
