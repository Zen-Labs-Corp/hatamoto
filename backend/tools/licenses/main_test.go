package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"Permission is hereby granted, free of charge, to any person":                                                 "MIT",
		"Apache License\n Version 2.0, January 2004":                                                                  "Apache-2.0",
		"Redistribution and use in source and binary forms ... Neither the name of Google":                            "BSD-3-Clause",
		"Redistribution and use in source and binary forms ... this list of conditions and the following disclaimer.": "BSD-2-Clause",
		"GNU GENERAL PUBLIC LICENSE Version 3":                                                                        "GPL",
		"GNU Affero General Public License":                                                                           "AGPL",
		"something custom":                                                                                            "UNKNOWN",
	}
	for text, want := range cases {
		if got := classify(text); got != want {
			t.Errorf("classify(%q) = %s, want %s", text, got, want)
		}
	}
	if permissive["GPL"] || permissive["UNKNOWN"] {
		t.Error("copyleft and unknown must not be permissive")
	}
}

func TestCheckNotice(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "NOTICE"), []byte("Example\nCopyright 2020 Example  authors\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := module{Path: "example.com/m", Version: "v1.0.0", Dir: dir, Files: []string{"LICENSE", "NOTICE"}}
	plain := module{Path: "example.com/plain", Dir: dir, Files: []string{"LICENSE"}}
	good := "Lucide (ISC)\nexample.com/m: Example Copyright 2020 Example authors"
	if p := checkNotice(good, []module{mod, plain}); len(p) != 0 {
		t.Errorf("good notice: %v", p)
	}
	// the module is named but its text is not reproduced
	if p := checkNotice("Lucide ISC example.com/m", []module{mod}); len(p) != 1 {
		t.Errorf("missing text: %v", p)
	}
	// the text is there but the module is not named
	if p := checkNotice("Lucide ISC Example Copyright 2020 Example authors", []module{mod}); len(p) != 1 {
		t.Errorf("missing name: %v", p)
	}
	// no Lucide attribution
	if p := checkNotice("example.com/m Example Copyright 2020 Example authors", nil); len(p) != 1 {
		t.Errorf("missing Lucide: %v", p)
	}
}

// A module that ships compiled C libraries (lib/LICENSE.*) gets those notices
// into the bundle without changing its own licence.
func TestBundleIncludesLibLicences(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	mit := "Permission is hereby granted, free of charge, to any person"
	for name, text := range map[string]string{"LICENSE": mit, "lib/LICENSE.libwebp": "Redistribution and use in source and binary forms", "lib/README": "x"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := module{Path: "example.com/wasm", Version: "v1.0.0", Dir: dir}
	if err := scan(&m); err != nil {
		t.Fatal(err)
	}
	if m.Licence != "MIT" {
		t.Errorf("module licence %s, want MIT", m.Licence)
	}
	out := filepath.Join(t.TempDir(), "bundle")
	if err := writeBundle(out, []module{m}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "example.com", "wasm", "lib", "LICENSE.libwebp")); err != nil {
		t.Errorf("lib licence missing from the bundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "example.com", "wasm", "lib", "README")); err == nil {
		t.Error("non-licence lib file copied")
	}
}
