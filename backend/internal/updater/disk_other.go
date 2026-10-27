//go:build !unix

package updater

import "errors"

// FreeDisk is not available on this platform; the disk check then blocks.
func FreeDisk(dir string) (uint64, error) {
	return 0, errors.New("free disk space is not available on this platform")
}
