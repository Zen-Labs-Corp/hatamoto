package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
)

// withLocalSource runs fn with the path of a local file holding the bytes of
// key. On the local disk that is the stored file itself; with another store
// the object is copied to a temporary file that is removed afterwards. fn must
// not keep or modify the path.
func (s *Service) withLocalSource(ctx context.Context, key string, fn func(path string) error) error {
	if s.local != nil {
		return fn(s.local.localPath(key))
	}
	body, err := s.store.Get(ctx, key)
	if err != nil {
		return err
	}
	defer body.Close()
	tmp, err := os.CreateTemp("", "hatamoto-media-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, body); err != nil {
		tmp.Close()
		return fmt.Errorf("copy %q to a temporary file: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return fn(tmp.Name())
}

// putSpooled copies r to a temporary file, stores that file under key and
// returns its path, which the caller removes (also when err is not nil and the
// path is not empty). It is how an upload reaches a store that has no local
// file to decode from.
func (s *Service) putSpooled(ctx context.Context, key string, r io.Reader) (path string, written int64, err error) {
	path, _, err = spoolTemp(r)
	if err != nil {
		return path, 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return path, 0, err
	}
	defer f.Close()
	written, err = s.store.Put(ctx, key, f)
	return path, written, err
}

// spoolTemp copies r to a new temporary file and returns its path and size.
// The path is returned (for the caller to remove) even when err is not nil.
func spoolTemp(r io.Reader) (path string, n int64, err error) {
	tmp, err := os.CreateTemp("", "hatamoto-media-*")
	if err != nil {
		return "", 0, err
	}
	n, err = io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	return tmp.Name(), n, err
}

// putFile stores the file at path under key.
func putFile(ctx context.Context, store BlobStore, key, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = store.Put(ctx, key, f)
	return err
}

// snapshot is a set of stored objects copied to temporary files, to put them
// back after a failed change.
type snapshot struct{ files map[string]string }

func (s *snapshot) has(key string) bool { _, ok := s.files[key]; return ok }

// snapshot copies each key that exists to a temporary file; a missing key is
// skipped. Call discard when done, also after an error.
func (s *Service) snapshot(ctx context.Context, keys []string) (*snapshot, error) {
	snap := &snapshot{files: map[string]string{}}
	for _, k := range keys {
		if snap.has(k) {
			continue
		}
		body, err := s.store.Get(ctx, k)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return snap, err
		}
		path, _, err := spoolTemp(body)
		body.Close()
		if path != "" {
			snap.files[k] = path
		}
		if err != nil {
			return snap, err
		}
	}
	return snap, nil
}

// restore puts every snapshotted object back.
func (s *snapshot) restore(ctx context.Context, store BlobStore, logger *slog.Logger) {
	for k, p := range s.files {
		if err := putFile(ctx, store, k, p); err != nil {
			logger.Error("failed to put a file back", slog.String("path", k), slog.String("error", err.Error()))
		}
	}
}

func (s *snapshot) discard() {
	for _, p := range s.files {
		os.Remove(p)
	}
}
