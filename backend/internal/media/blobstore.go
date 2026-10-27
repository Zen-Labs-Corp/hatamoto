package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// BlobStore holds the bytes of stored media. Keys are the persisted
// StoragePath strings, relative to the store and never absolute.
type BlobStore interface {
	// Put streams r into key and returns the bytes written. A failed Put
	// leaves no partial blob behind.
	Put(ctx context.Context, key string, r io.Reader) (int64, error)
	// Get opens key for streaming; close the returned reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes key. A missing key returns an error satisfying
	// errors.Is(err, fs.ErrNotExist).
	Delete(ctx context.Context, key string) error
	// URL is the public URL of key.
	URL(key string) string
}

// localStore is the local-disk driver. Reads, deletes and the directory
// checks go through an os.Root, which refuses symlinks that lead out of it.
// The final file create of Put goes through the createUpload test seam, which
// takes a path, so Put vets the target through the root first.
type localStore struct{ root, baseURL string }

// NewLocalStore returns the local-disk store rooted at root (created if
// missing, symlinks resolved once) whose URLs start with baseURL.
func NewLocalStore(root, baseURL string) (BlobStore, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("create blob root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve blob root: %w", err)
	}
	return &localStore{root: resolved, baseURL: baseURL}, nil
}

func (l *localStore) URL(key string) string { return l.baseURL + "/" + key }

// localPath is the on-disk path of key. It is the local-only escape hatch for
// the image helpers, which decode from a path.
func (l *localStore) localPath(key string) string { return filepath.Join(l.root, key) }

func (l *localStore) openRoot() (*os.Root, error) { return os.OpenRoot(l.root) }

// checkKey refuses keys that are absolute or climb out with "..".
func checkKey(key string) error {
	if key == "" || filepath.IsAbs(key) || strings.HasPrefix(key, "/") {
		return fmt.Errorf("invalid blob key %q", key)
	}
	for _, part := range strings.FieldsFunc(key, func(r rune) bool { return r == '/' || r == filepath.Separator }) {
		if part == ".." {
			return fmt.Errorf("invalid blob key %q", key)
		}
	}
	return nil
}

func (l *localStore) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	if err := checkKey(key); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	root, err := l.openRoot()
	if err != nil {
		return 0, err
	}
	defer root.Close()
	// Create the directories through the root: a symlinked directory that
	// leaves it (to another disk) is refused here instead of letting the write
	// succeed and every read fail.
	if err := root.MkdirAll(filepath.Dir(key), 0755); err != nil {
		return 0, fmt.Errorf("create blob directory: %w", err)
	}
	// A symlink at the file itself would be followed by the create below.
	if fi, err := root.Lstat(key); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return 0, fmt.Errorf("blob %q is a symlink", key)
	}
	full := l.localPath(key)
	out, err := createUpload(full)
	if err != nil {
		return 0, fmt.Errorf("create blob: %w", err)
	}
	written, err := io.Copy(out, r)
	// Close reports a deferred write failure (full disk, network filesystem):
	// a file that did not close cleanly may be truncated, so it is not kept.
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(full)
		return 0, err
	}
	return written, nil
}

// openFile opens key as an *os.File, for callers that need Stat and Seek.
func (l *localStore) openFile(key string) (*os.File, error) {
	root, err := l.openRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(key)
}

func (l *localStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f, err := l.openFile(key)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (l *localStore) Delete(_ context.Context, key string) error {
	root, err := l.openRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(key); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("delete blob %q: %w", key, fs.ErrNotExist)
		}
		return err
	}
	return nil
}
