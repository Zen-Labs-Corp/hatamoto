package designcheck

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update rewrites generated docs (TestDesignContractDocsTable): go test ./internal/admin/designcheck -update
var update = flag.Bool("update", false, "rewrite generated files")

// webAdmin is backend/web/admin, the tree every check here reads.
var webAdmin = filepath.Join("..", "..", "..", "web", "admin")

// adminAsset reads a file under backend/web/admin from path segments.
func adminAsset(t *testing.T, rel ...string) string {
	t.Helper()
	return readSrc(t, filepath.Join(append([]string{webAdmin}, rel...)...))
}

// readWeb reads a file under backend/web/admin from a slash path.
func readWeb(t *testing.T, p string) string {
	t.Helper()
	return readSrc(t, filepath.Join(webAdmin, filepath.FromSlash(p)))
}

// readSrc reads a file by path.
func readSrc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
