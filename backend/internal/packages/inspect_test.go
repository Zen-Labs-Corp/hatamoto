package packages

import (
	"encoding/hex"
	"strings"
	"testing"
)

const inspectPluginJSON = `{"name":"acme-hello","version":"1.0.0","sdk":"^0.21","description":"Says hello","allowed_hosts":["api.example.com"],"permissions":["http.fetch"]}`

func inspectPluginFiles() map[string][]byte {
	return map[string][]byte{PluginManifest: []byte(inspectPluginJSON), PluginWasm: []byte("\x00asm\x01\x00\x00\x00")}
}

func hasProblem(problems []string, sub string) bool {
	for _, p := range problems {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}

func TestInspectSignedPlugin(t *testing.T) {
	k := newKey(t)
	raw, err := PackFiles(inspectPluginFiles(), &k)
	if err != nil {
		t.Fatal(err)
	}
	res := Inspector{}.Inspect(raw, k.Public)
	if !res.SignatureOK || len(res.Problems) != 0 || res.Problems == nil {
		t.Fatalf("result = %+v", res)
	}
	sum := parse(t, raw).SHA256()
	if res.Kind != "plugin" || res.Name != "acme-hello" || res.Version != "1.0.0" ||
		res.SHA256 != hex.EncodeToString(sum[:]) || res.Size != int64(len(raw)) ||
		res.SDK != "^0.21" || res.Description != "Says hello" || res.SignerKeyID != k.ID ||
		len(res.Permissions) != 1 || res.Permissions[0] != "http.fetch" ||
		len(res.AllowedHosts) != 1 || res.AllowedHosts[0] != "api.example.com" {
		t.Fatalf("result = %+v", res)
	}
}

func TestInspectOtherKeyNamesRealSigner(t *testing.T) {
	k, other := newKey(t), newKey(t)
	raw, _ := PackFiles(inspectPluginFiles(), &k)
	res := Inspector{}.Inspect(raw, other.Public)
	if res.SignatureOK || !hasProblem(res.Problems, k.ID) || res.SignerKeyID != k.ID {
		t.Fatalf("result = %+v", res)
	}
}

func TestInspectTamperedPackage(t *testing.T) {
	k := newKey(t)
	signed, _ := PackFiles(themeFiles(), &k)
	sigOf := parse(t, signed).Signature
	files := themeFiles()
	files["assets/css/base.css"] = []byte("body{display:none}")
	files[SignatureFile] = []byte(`{"signer":"` + sigOf.Signer + `","algorithm":"ed25519","signature":"` + sigOf.Signature + `"}`)
	raw, err := zipFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	res := Inspector{}.Inspect(raw, k.Public)
	if res.SignatureOK || !hasProblem(res.Problems, "does not match") {
		t.Fatalf("result = %+v", res)
	}
}

func TestInspectUnsignedAndMissingKey(t *testing.T) {
	k := newKey(t)
	raw, _ := PackFiles(themeFiles(), nil)
	if res := (Inspector{}).Inspect(raw, k.Public); res.SignatureOK || !hasProblem(res.Problems, "unsigned") || res.SignerKeyID != "" {
		t.Fatalf("unsigned: %+v", res)
	}
	signed, _ := PackFiles(themeFiles(), &k)
	if res := (Inspector{}).Inspect(signed, ""); res.SignatureOK || !hasProblem(res.Problems, "no signer key") {
		t.Fatalf("no key: %+v", res)
	}
	if res := (Inspector{}).Inspect(signed, "!!!"); res.SignatureOK || !hasProblem(res.Problems, "signer_key") {
		t.Fatalf("bad key: %+v", res)
	}
}

func TestInspectTheme(t *testing.T) {
	k := newKey(t)
	raw, _ := PackFiles(themeFiles(), &k)
	res := Inspector{}.Inspect(raw, k.Public)
	if !res.SignatureOK || res.Kind != "theme" || res.Name != "sunrise" || res.Version != "1.2.3" {
		t.Fatalf("result = %+v", res)
	}
}

func TestInspectGarbageAndOverLimit(t *testing.T) {
	res := Inspector{}.Inspect([]byte("not a zip"), "")
	if res.SignatureOK || len(res.Problems) != 1 || res.Size != 9 || res.SHA256 == "" || res.Kind != "" {
		t.Fatalf("garbage: %+v", res)
	}
	k := newKey(t)
	raw, _ := PackFiles(themeFiles(), &k)
	small := Limits{Archive: 16}
	in := Inspector{Limits: small}
	if in.ArchiveLimit() != 16 || (Inspector{}).ArchiveLimit() != DefaultLimits.Archive {
		t.Fatal("ArchiveLimit")
	}
	res = in.Inspect(raw, k.Public)
	if res.SignatureOK || res.Name != "" || !hasProblem(res.Problems, "too large: over 16 bytes") {
		t.Fatalf("over limit: %+v", res)
	}
}

func TestInspectorFillsZeroLimitsFromTheDefaults(t *testing.T) {
	k := newKey(t)
	raw, err := PackFiles(inspectPluginFiles(), &k)
	if err != nil {
		t.Fatal(err)
	}
	in := Inspector{Limits: Limits{Archive: 1 << 20}}
	if got := in.ArchiveLimit(); got != 1<<20 {
		t.Fatalf("archive limit = %d", got)
	}
	if res := in.Inspect(raw, k.Public); !res.SignatureOK || len(res.Problems) != 0 {
		t.Fatalf("partial limits refused a real package: %+v", res)
	}
}
