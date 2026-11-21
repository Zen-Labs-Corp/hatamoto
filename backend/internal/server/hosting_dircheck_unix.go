//go:build unix

package server

import (
	"os"
	"path/filepath"
	"syscall"

	"hatamoto.dev/core/internal/admin"
)

// folderState reports whether the export folder is ready: it exists and can be
// written, or it is missing and its parent can be written, so the first export
// creates it.
func folderState(dir string) string {
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
		if syscall.Access(filepath.Dir(dir), 2 /* W_OK */) != nil {
			return admin.HostingCheckNotWritable
		}
		return admin.HostingCheckMissing
	}
	return admin.HostingCheckUnknown
}
