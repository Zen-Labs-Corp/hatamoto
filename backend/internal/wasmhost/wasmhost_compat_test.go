package wasmhost

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// packagesDirEnv overrides the directory searched for first-party packages.
const packagesDirEnv = "HATAMOTO_TEST_PACKAGES_DIR"

// TestFirstPartyPackagesLoadUnderTableLimits guards that the table and
// reference-type restrictions do not reject the modules shipped with the
// product. It skips when the package directory is absent.
func TestFirstPartyPackagesLoadUnderTableLimits(t *testing.T) {
	dir := os.Getenv(packagesDirEnv)
	if dir == "" {
		dir = "../../../registry/packages"
	}
	loadWasm := func(t *testing.T, name string, wasm []byte) {
		t.Helper()
		if err := checkTables(wasm); err != nil {
			t.Fatalf("checkTables: %v", err)
		}
		h := newHost(t)
		if err := h.load(context.Background(), manifest(name, sdk.Limits{}), wasm); err != nil {
			t.Fatalf("load: %v", err)
		}
	}

	t.Run("legacy", func(t *testing.T) {
		wasm, err := os.ReadFile("../pluginhost/testdata/legacy/legacy.wasm")
		if err != nil {
			t.Skipf("legacy.wasm: %v", err)
		}
		loadWasm(t, "legacy", wasm)
	})

	matches, _ := filepath.Glob(filepath.Join(dir, "*.hmp"))
	if len(matches) == 0 {
		t.Skipf("no packages under %s", dir)
	}
	loaded := 0
	for _, path := range matches {
		name := strings.TrimSuffix(filepath.Base(path), ".hmp")
		t.Run(name, func(t *testing.T) {
			zr, err := zip.OpenReader(path)
			if err != nil {
				t.Fatal(err)
			}
			defer zr.Close()
			for _, f := range zr.File {
				if f.Name != "plugin.wasm" {
					continue
				}
				rc, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				wasm, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					t.Fatal(err)
				}
				loadWasm(t, "pkg", wasm)
				loaded++
				return
			}
			t.Skip("package has no plugin.wasm")
		})
	}
	t.Logf("loaded %d package modules", loaded)
}
