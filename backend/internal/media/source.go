package media

import (
	"context"
	"fmt"
	"io"
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
	tmp, err := os.CreateTemp("", "hatamoto-media-*")
	if err != nil {
		return "", 0, err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, r); err != nil {
		return tmp.Name(), 0, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return tmp.Name(), 0, err
	}
	written, err = s.store.Put(ctx, key, tmp)
	return tmp.Name(), written, err
}
