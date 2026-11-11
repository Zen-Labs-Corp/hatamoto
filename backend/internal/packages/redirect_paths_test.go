package packages

import (
	"errors"
	"strings"
	"testing"
)

// pathsPack is a plugin package declaring the given request.redirect paths.
func pathsPack(name, version string, paths ...string) map[string][]byte {
	list := `"` + strings.Join(paths, `","`) + `"`
	if len(paths) == 0 {
		list = ""
	}
	// A widget keeps the package a valid plugin without a wasm module.
	files := widgetPack(name, map[string][4]string{"banner": {wSchema, wHTML, "", ""}})
	files[PluginManifest] = []byte(`{"name":"` + name + `","version":"` + version + `","sdk":"^0.22","permissions":["builder.island"],"redirect_paths":[` + list + `]}`)
	return files
}

func parsedPaths(t *testing.T, name string, paths ...string) *Package {
	t.Helper()
	raw, err := PackFiles(pathsPack(name, "1.0.0", paths...), nil)
	if err != nil {
		t.Fatal(err)
	}
	return parse(t, raw)
}

func rowFor(t *testing.T, name string, enabled bool, state string, paths ...string) Row {
	t.Helper()
	p := parsedPaths(t, name, paths...)
	return Row{Kind: KindPlugin, Name: name, Manifest: p.Manifest, Enabled: enabled, State: state}
}

func TestRedirectPathConflict(t *testing.T) {
	rows := []Row{
		rowFor(t, "one", true, StateInstalled, "/go", "/start"),
		rowFor(t, "off", false, StateInstalled, "/off"),
		rowFor(t, "gone", true, StateUninstalling, "/gone"),
		{Kind: KindTheme, Name: "skin", Manifest: []byte(`{"redirect_paths":["/theme"]}`)},
	}
	cases := []struct {
		name  string
		pack  *Package
		taken string
	}{
		{"enabled owner", parsedPaths(t, "two", "/start"), "one"},
		{"disabled owner counts", parsedPaths(t, "two", "/off"), "off"},
		{"trailing slash is distinct", parsedPaths(t, "two", "/go/"), ""},
		{"same plugin updating", parsedPaths(t, "one", "/go"), ""},
		{"uninstalling owner is ignored", parsedPaths(t, "two", "/gone"), ""},
		{"theme rows are ignored", parsedPaths(t, "two", "/theme"), ""},
		{"no paths", parsedPaths(t, "two"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := redirectPathConflict(rows, tc.pack)
			if tc.taken == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, ErrRedirectPathTaken) || !strings.Contains(err.Error(), `"`+tc.taken+`"`) {
				t.Fatalf("err = %v, want ErrRedirectPathTaken naming %q", err, tc.taken)
			}
		})
	}
}
