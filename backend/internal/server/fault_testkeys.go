//go:build hatamoto_testkeys

package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// wrapFault makes GET / answer 500 when HATAMOTO_TEST_FAULT is root-500,
// optionally "root-500@v1.0.1" to hit only the binary that runs from a
// directory of that name (see app.faultAfterMigrate). It exercises the
// supervisor's smoke gate. Test builds only.
func wrapFault(h http.Handler) http.Handler {
	kind, only, _ := strings.Cut(os.Getenv("HATAMOTO_TEST_FAULT"), "@")
	if kind != "root-500" {
		return h
	}
	if exe, err := os.Executable(); only != "" && (err != nil || filepath.Base(filepath.Dir(exe)) != only) {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Error(w, "injected fault", http.StatusInternalServerError)
			return
		}
		h.ServeHTTP(w, r)
	})
}
