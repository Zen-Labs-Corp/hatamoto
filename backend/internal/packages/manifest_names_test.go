package packages

import "testing"

const nameBase = `"name":"hello","version":"1.0.0","sdk":"^0.7"`

// A plugin.json whose member names read differently under v1 and v2 name
// folding is refused, so the shown, stored and enforced manifests agree.
func TestPluginManifestAmbiguousNames(t *testing.T) {
	for name, extra := range map[string]string{
		"both spellings":   `"allowed_hosts":["evil.example"],"allowedHosts":["api.example"]`,
		"camel only":       `"allowedHosts":["api.example"]`,
		"dash header":      `"request-headers":["x-a"]`,
		"repeated name":    `"permissions":["http.fetch"],"permissions":["http.fetch.any"]`,
		"case-variant dup": `"allowed_hosts":["a.example"],"ALLOWED_HOSTS":["b.example"]`,
	} {
		t.Run(name, func(t *testing.T) {
			m := "{" + nameBase + "," + extra + "}"
			raw, err := zipFiles(map[string][]byte{PluginManifest: []byte(m), PluginWasm: []byte("\x00asm\x01\x00\x00\x00")})
			if err != nil {
				t.Fatal(err)
			}
			mustRefuse(t, raw, PluginManifest)
		})
	}
}

// An installed manifest read from the database does not bind a name that only
// the v2 folding would match, so widenedAccess cannot be bypassed.
func TestWidenedAccessStoredSpellings(t *testing.T) {
	p := parse(t, mustPack(t, `{`+nameBase+`,"allowed_hosts":["evil.example"]}`))
	stored := []byte(`{` + nameBase + `,"allowedHosts":["evil.example"]}`)
	hosts, _, _, _ := widenedAccess(stored, p)
	if len(hosts) != 1 || hosts[0] != "evil.example" {
		t.Fatalf("hosts = %v, want the host asked for confirmation", hosts)
	}
	if h := newRequestHeaders([]byte(`{`+nameBase+`,"request-headers":["x-a"]}`), parse(t, mustPack(t, `{`+nameBase+`,"request_headers":["x-a"]}`))); len(h) != 1 {
		t.Fatalf("request headers = %v", h)
	}
}

func mustPack(t *testing.T, manifest string) []byte {
	t.Helper()
	raw, err := zipFiles(map[string][]byte{PluginManifest: []byte(manifest), PluginWasm: []byte("\x00asm\x01\x00\x00\x00")})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
