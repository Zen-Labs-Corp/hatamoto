//go:build !unix

package server

import "hatamoto.dev/core/internal/admin"

// folderState is not checked on this platform.
func folderState(string) string { return admin.HostingCheckUnknown }
