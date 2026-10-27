package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"hatamoto.dev/core/internal/packages"
)

// createNew writes data to a new file; it never overwrites.
func createNew(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

// cmdKeygen writes <kind>.key (0600) and <kind>.pub (0644) in the format of
// `hatamoto keys generate`. Moving the .pub into internal/release/keys is a
// reviewed commit (RUNBOOK section 7).
func cmdKeygen(args []string, stdout, stderr io.Writer, _ env) error {
	var kind, dir string
	fs := newFlags("keygen", stderr)
	fs.StringVar(&kind, "kind", "", "release or manifest")
	fs.StringVar(&dir, "out", ".", "directory to write to")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if kind != "release" && kind != "manifest" {
		return fmt.Errorf("--kind must be release or manifest, not %q", kind)
	}
	k, err := packages.GenerateKey()
	if err != nil {
		return err
	}
	priv, pub := filepath.Join(dir, kind+".key"), filepath.Join(dir, kind+".pub")
	secret, _ := json.MarshalIndent(k, "", "  ")
	public, _ := json.MarshalIndent(packages.Key{ID: k.ID, Public: k.Public}, "", "  ")
	if err := createNew(priv, append(secret, '\n'), 0o600); err != nil {
		return err
	}
	if err := createNew(pub, append(public, '\n'), 0o644); err != nil {
		os.Remove(priv)
		return err
	}
	fmt.Fprintf(stdout, "key id:     %s\nsecret key: %s\npublic key: %s\n", k.ID, priv, pub)
	return nil
}

// loadKeyFile reads a packages.Key JSON file and checks it carries a usable
// private half.
func loadKeyFile(path string) (packages.Key, error) {
	var k packages.Key
	b, err := os.ReadFile(path)
	if err != nil {
		return k, err
	}
	if err := json.Unmarshal(b, &k); err != nil {
		return k, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := k.PrivateKey(); err != nil {
		return k, fmt.Errorf("%s: %w", path, errors.New("not a private key file"))
	}
	return k, nil
}
