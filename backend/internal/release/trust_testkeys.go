//go:build hatamoto_testkeys

package release

import (
	"os"
	"path/filepath"
)

// Test-only trust seam, compiled in only with -tags hatamoto_testkeys. It lets
// an end-to-end run verify records signed by throwaway keys with a binary built
// from this tree. The directory holds public key files only (release-*.pub,
// manifest.pub). Default builds contain none of this; TestDefaultBuildHasNoTestSeams
// proves it. init order: trust.go's init runs first (files sort by name).
func init() {
	dir := os.Getenv("HATAMOTO_TEST_TRUST_DIR")
	if dir == "" {
		return
	}
	dir = filepath.Clean(dir)
	r, m, err := loadTrust(os.DirFS(filepath.Dir(dir)), filepath.Base(dir))
	if err != nil {
		panic(err)
	}
	embeddedRelease, embeddedManifest = r, m
}
