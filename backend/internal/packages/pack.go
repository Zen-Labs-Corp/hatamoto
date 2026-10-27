package packages

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PackDir builds a .hmp from a plugin or theme directory. Only the files a
// package may hold are included (dot files, node_modules and the like are
// skipped, not refused). With a key the archive is signed. The result is
// checked with Parse before it is returned, so what is built is installable.
func PackDir(dir string, key *Key) ([]byte, error) {
	root := os.DirFS(dir)
	files := map[string][]byte{}
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if !strings.Contains(p, "/") && !topDirs[p] {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: not a regular file (links are not packed)", p)
		}
		if !strings.Contains(p, "/") && (!topFiles[p] || p == SignatureFile) {
			return nil
		}
		if rest, ok := strings.CutPrefix(p, widgetsDir+"/"); ok && !widgetFileSet[path.Base(rest)] {
			return nil // a stray file next to a widget's own files is not packed
		}
		b, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		files[p] = b
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", dir, err)
	}
	return PackFiles(files, key)
}

// PackFiles builds a .hmp from a file map, deterministically (sorted, fixed
// timestamps), signing it when key is set.
func PackFiles(in map[string][]byte, key *Key) ([]byte, error) {
	files := make(map[string][]byte, len(in))
	for k, v := range in {
		if k != SignatureFile { // never ship a signature the caller did not just make
			files[k] = v
		}
	}
	unsigned, err := zipFiles(files)
	if err != nil {
		return nil, err
	}
	p, err := Parse(unsigned, DefaultLimits)
	if err != nil {
		return nil, err
	}
	out := unsigned
	if key != nil {
		sig, err := SignBytes(*key, p.Digest[:])
		if err != nil {
			return nil, err
		}
		sigJSON, err := marshalIndented(sig)
		if err != nil {
			return nil, err
		}
		withSig := map[string][]byte{SignatureFile: sigJSON}
		for k, v := range files {
			withSig[k] = v
		}
		if out, err = zipFiles(withSig); err != nil {
			return nil, err
		}
		if _, err := Parse(out, DefaultLimits); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func zipFiles(files map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: filepath.ToSlash(n), Method: zip.Deflate, Modified: time.Unix(0, 0).UTC()})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(files[n]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
