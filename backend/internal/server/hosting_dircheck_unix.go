//go:build unix

package server

import (
	"os"
	"path/filepath"
	"syscall"

	"hatamoto.dev/core/internal/admin"
)

// folderState reports whether the export folder is ready. The export stages a
// copy next to the folder and swaps it in by rename, so the parent must always
// be writable; an existing folder must be writable too. A missing folder is
// created by the first export.
func folderState(dir string) string {
	if syscall.Access(filepath.Dir(dir), 2 /* W_OK */) != nil {
		return admin.HostingCheckNotWritable
	}
	fi, err := os.Stat(dir)
	switch {
	case err == nil && !fi.IsDir():
		return admin.HostingCheckNotWritable
	case err == nil:
		if syscall.Access(dir, 2 /* W_OK */) != nil {
			return admin.HostingCheckNotWritable
		}
		return admin.HostingCheckOK
	case os.IsNotExist(err):
		return admin.HostingCheckMissing
	}
	return admin.HostingCheckUnknown
}
