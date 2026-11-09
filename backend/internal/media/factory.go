package media

import (
	"context"
	"io"
	"log/slog"

	"hatamoto.dev/core/internal/config"
)

// NewStoreFromEnv returns the object storage store selected by MEDIA_STORAGE,
// or nil for local disk (the default). It does not touch the network, so an
// unreachable bucket never prevents a start; only an invalid configuration
// (missing variables, bad endpoint, unknown driver) is an error.
func NewStoreFromEnv(getenv func(string) string, baseURL string) (BlobStore, error) {
	kind, err := StorageFromEnv(getenv)
	if err != nil {
		return nil, err
	}
	if kind != "s3" {
		return nil, nil
	}
	target, err := S3TargetFromEnv(getenv)
	if err != nil {
		return nil, err
	}
	store, err := NewS3Store(target, baseURL, nil)
	if err != nil {
		return nil, err
	}
	return store, nil
}

// ConfigFromServer builds the media configuration every caller shares, so no
// path writes to local disk on an installation that uses object storage. The
// server settings only override the defaults when set. On an invalid object
// storage configuration the error is returned together with a Config whose
// Store fails every call.
func ConfigFromServer(s config.ServerConfig, getenv func(string) string, log *slog.Logger) (Config, error) {
	c := DefaultConfig()
	if s.MediaUploadDir != "" {
		c.UploadDir = s.MediaUploadDir
	}
	if s.MediaBaseURL != "" {
		c.BaseURL = s.MediaBaseURL
	}
	if s.MediaMaxSizeMB > 0 {
		c.MaxSizeMB = s.MediaMaxSizeMB
	}
	store, err := NewStoreFromEnv(getenv, c.BaseURL)
	if err != nil {
		if log != nil {
			log.Error("media: object storage is not configured", "err", err)
		}
		c.Store = UnavailableStore(err)
		return c, err
	}
	c.Store = store
	return c, nil
}

// UnavailableStore is the store of an installation whose object storage
// configuration is invalid and could not stop the start: every operation
// fails with err, so nothing is silently written to local disk.
func UnavailableStore(err error) BlobStore { return unavailableStore{err: err} }

type unavailableStore struct{ err error }

func (u unavailableStore) Put(context.Context, string, io.Reader) (int64, error) { return 0, u.err }
func (u unavailableStore) Get(context.Context, string) (io.ReadCloser, error)    { return nil, u.err }
func (u unavailableStore) Delete(context.Context, string) error                  { return u.err }
func (u unavailableStore) URL(key string) string                                 { return "/media/" + key }
