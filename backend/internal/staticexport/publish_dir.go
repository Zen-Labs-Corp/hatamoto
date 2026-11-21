package staticexport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// rename is os.Rename; tests replace it to fail the swap.
var rename = os.Rename

// DirGuard lists directories an export must never replace or contain: the
// data directory and the media directory.
type DirGuard struct {
	Forbidden []string
}

// DirTarget publishes to a local directory by swapping it in atomically and
// keeping the previous tree at <out>.prev.
type DirTarget struct {
	out string
}

// NewDirTarget checks out against the guard. out must be absolute, must not
// be "/", must not equal, sit inside or contain a forbidden directory, and
// an existing non-empty directory must be an earlier export (it holds the
// manifest file). The same holds for an existing <out>.prev.
func NewDirTarget(out string, g DirGuard) (*DirTarget, error) {
	if !filepath.IsAbs(out) {
		return nil, fmt.Errorf("output directory %q must be an absolute path", out)
	}
	out = filepath.Clean(out)
	if out == string(filepath.Separator) {
		return nil, errors.New("refusing to export to /")
	}
	real := resolve(out)
	for _, f := range g.Forbidden {
		if f == "" {
			continue
		}
		f = resolve(f)
		if real == f || within(real, f) || within(f, real) {
			return nil, fmt.Errorf("output directory %s overlaps %s, which the export must not touch", out, f)
		}
	}
	info, err := os.Stat(out)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	case !info.IsDir():
		return nil, fmt.Errorf("output path %s exists and is not a directory", out)
	default:
		entries, err := os.ReadDir(out)
		if err != nil {
			return nil, err
		}
		if len(entries) > 0 {
			if _, err := os.Stat(filepath.Join(out, ManifestFile)); err != nil {
				return nil, fmt.Errorf("output directory %s is not empty and was not written by a static export (no %s)", out, ManifestFile)
			}
		}
	}
	if err := checkPrev(out + ".prev"); err != nil {
		return nil, err
	}
	return &DirTarget{out: out}, nil
}

// checkPrev refuses an existing <out>.prev that an export did not write: the
// swap replaces it, so anything else there would be deleted.
func checkPrev(prev string) error {
	info, err := os.Lstat(prev)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(prev)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return nil
		}
		if _, err := os.Stat(filepath.Join(prev, ManifestFile)); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%s exists and was not written by a static export (no %s); the export would replace it, so move it away first", prev, ManifestFile)
}

// resolve makes p absolute and follows symlinks as far as the path exists.
func resolve(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	rest := ""
	for cur := p; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// within reports whether p is strictly inside dir.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (t *DirTarget) Key() string      { return "dir:" + t.out }
func (t *DirTarget) Describe() string { return t.out }

// Stage creates a sibling temp directory, so the final rename stays on one
// filesystem.
func (t *DirTarget) Stage(context.Context) (string, error) {
	parent := filepath.Dir(t.out)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(parent, "."+filepath.Base(t.out)+".tmp-*")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func (t *DirTarget) Previous(context.Context) (Manifest, bool, error) {
	f, err := os.Open(filepath.Join(t.out, ManifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, err
	}
	defer f.Close()
	m, err := ReadManifest(f)
	if err != nil {
		return Manifest{}, false, err
	}
	return m, true, nil
}

// Publish writes the manifest into the staged tree, then swaps it in: the
// old tree moves to <out>.prev (replacing an older .prev) and the staged
// tree takes its place. If the second rename fails the old tree is put back.
func (t *DirTarget) Publish(_ context.Context, staged string, _ Diff, next Manifest) error {
	body, err := next.Encode()
	if err != nil {
		return err
	}
	if err := writeSynced(filepath.Join(staged, ManifestFile), body); err != nil {
		return err
	}
	prev := t.out + ".prev"
	if err := os.RemoveAll(prev); err != nil {
		return err
	}
	_, statErr := os.Stat(t.out)
	hadOut := statErr == nil
	if hadOut {
		if err := rename(t.out, prev); err != nil {
			return fmt.Errorf("move %s aside: %w", t.out, err)
		}
	}
	if err := rename(staged, t.out); err != nil {
		if hadOut {
			if rerr := rename(prev, t.out); rerr != nil {
				return fmt.Errorf("publish %s: %w (restoring the previous tree from %s also failed: %v)", t.out, err, prev, rerr)
			}
		}
		return fmt.Errorf("publish %s: %w (previous tree restored)", t.out, err)
	}
	return nil
}

func (t *DirTarget) Discard(staged string) { _ = os.RemoveAll(staged) }

func writeSynced(name string, body []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(body)
	if err == nil {
		_ = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
