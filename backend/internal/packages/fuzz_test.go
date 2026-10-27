package packages

import (
	"testing"
	"time"
)

// FuzzParse feeds arbitrary bytes to the package parser. Whatever the input, it
// must return a package or an error, never panic or read past the limits.
func FuzzParse(f *testing.F) {
	for _, files := range []map[string][]byte{pluginFiles(), themeFiles()} {
		raw, err := PackFiles(files, nil)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add(rawZip(f, hdr("../evil", "x")))
	f.Add(rawZip(f, hdr(PluginManifest, "{")))
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := Parse(raw, DefaultLimits)
		if err != nil {
			return
		}
		// A parsed package must be readable end to end.
		_ = p.SHA256()
		_, _ = p.PluginManifest()
		_, _ = p.ThemeMeta()
	})
}

// FuzzReadArchive feeds arbitrary bytes to the registry archive reader.
func FuzzReadArchive(f *testing.F) {
	raw, err := PackFiles(pluginFiles(), nil)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(raw, shaOf(raw))
	f.Add(raw, "")
	f.Add([]byte("PK\x05\x06"+string(make([]byte, 18))), "")
	f.Fuzz(func(t *testing.T, data []byte, want string) {
		_, _ = ReadArchive(data, want, DefaultLimits)
	})
}

// FuzzParseIndex feeds arbitrary documents to the registry index parser: the
// signature check comes first, and nothing past it may panic either.
func FuzzParseIndex(f *testing.F) {
	key := newKey(f)
	ts := trust(f, key)
	doc := signedIndex(f, key, nil)
	f.Add(doc)
	f.Add([]byte(`{"index":{"version":1},"signer":"","algorithm":"","signature":""}`))
	f.Add([]byte(`{`))
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, doc []byte) {
		_, _ = ParseIndex(doc, ts, now)
	})
}
