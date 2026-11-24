package packages

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// prefixPack is a plugin package declaring one page path prefix.
func prefixPack(name, prefix string) map[string][]byte {
	files := widgetPack(name, map[string][4]string{"banner": {wSchema, wHTML, "", ""}})
	files[PluginManifest] = []byte(`{"name":"` + name + `","version":"1.0.0","sdk":"^0.27","permissions":["builder.island"],"page_path_prefixes":["` + prefix + `"]}`)
	return files
}

func TestInstallRefusesPrefix(t *testing.T) {
	for _, p := range []string{"/", "/p/", "/api/", "/uploads/", "/theme/", "/-/", "/admin/"} {
		// "/" is refused by the SDK shape check already while packing; the rest
		// reach the host refusal inside Install. Either error names the prefix.
		raw, err := PackFiles(prefixPack("pfx", p), nil)
		if err == nil {
			m := &Manager{Options: Options{AllowDev: true}}
			_, err = m.Install(context.Background(), raw, InstallRequest{Source: SourceDev})
		}
		if err == nil || !strings.Contains(err.Error(), strconv.Quote(p)) {
			t.Errorf("%s: err = %v, want a refusal naming it", p, err)
		}
	}
}
