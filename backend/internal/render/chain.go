package render

import (
	"errors"
	"io/fs"
	"path"
	"sort"
)

// Chain is an ordered lookup over several file systems. Index 0 wins: the
// conventional order is site override, disk theme, embedded default.
type Chain []fs.FS

// Open returns the first hit for name, so Chain satisfies fs.FS.
func (c Chain) Open(name string) (fs.File, error) {
	for _, fsys := range c {
		f, err := fsys.Open(name)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

// ReadFile returns the contents of the highest-precedence file called name.
func (c Chain) ReadFile(name string) ([]byte, error) {
	for _, fsys := range c {
		b, err := fs.ReadFile(fsys, name)
		if err == nil {
			return b, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
}

// Exists reports whether any layer holds name.
func (c Chain) Exists(name string) bool {
	_, err := c.Open(name)
	return err == nil
}

// Glob returns the sorted union of pattern matches across all layers; a name
// present in several layers appears once (ReadFile resolves its precedence).
func (c Chain) Glob(pattern string) ([]string, error) {
	seen := map[string]struct{}{}
	for _, fsys := range c {
		matches, err := fs.Glob(fsys, pattern)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			seen[m] = struct{}{}
		}
	}
	return sortedKeys(seen), nil
}

// Files returns every regular file name across all layers, sorted and unique.
func (c Chain) Files() ([]string, error) {
	seen := map[string]struct{}{}
	for _, fsys := range c {
		err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				seen[path.Clean(p)] = struct{}{}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return sortedKeys(seen), nil
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
