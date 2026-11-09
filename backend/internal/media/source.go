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
