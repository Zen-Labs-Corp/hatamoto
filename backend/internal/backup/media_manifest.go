package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/media"
)

// mediaManifestName is the archive member listing the media objects of a site
// whose media lives in object storage: what the database references, with the
// size and sha256 each object had when the backup ran.
const mediaManifestName = "media-manifest.json"

const mediaManifestVersion = 1

type mediaManifest struct {
	Version int                 `json:"version"`
	Files   []mediaManifestFile `json:"files"`
}

type mediaManifestFile struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// writeMediaManifest stats every key in the store and writes the manifest to
// path. Keys the store does not have are left out and returned. The checksum
// comes from the object's metadata, or is computed by reading the object.
func writeMediaManifest(ctx context.Context, store media.BlobStore, keys []string, path string) (missing []string, err error) {
	st, ok := store.(media.Stater)
	if !ok {
		return nil, errors.New("the media store cannot report object sizes")
	}
	mm := mediaManifest{Version: mediaManifestVersion, Files: []mediaManifestFile{}}
	for _, key := range keys {
		info, err := st.Stat(ctx, key)
		if errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, key)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		sum := info.SHA256
		if sum == "" {
			if sum, err = hashObject(ctx, store, key); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					missing = append(missing, key)
					continue
				}
				return nil, fmt.Errorf("%s: %w", key, err)
			}
		}
		mm.Files = append(mm.Files, mediaManifestFile{Key: key, Size: info.Size, SHA256: sum})
	}
	body, err := jsonx.Indent(mm)
	if err != nil {
		return nil, err
	}
	return missing, os.WriteFile(path, body, 0o600)
}

func hashObject(ctx context.Context, store media.BlobStore, key string) (string, error) {
	rc, err := store.Get(ctx, key)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// maxMediaProblemsListed caps the object keys named per kind of problem.
const maxMediaProblemsListed = 10

// mediaCheck is the result of comparing a media manifest with a store.
type mediaCheck struct {
	Total      int
	Missing    []string // keys not in the store
	Mismatched []string // keys whose size or sha256 differ
	// Unverified is set when the store could not be read; the other fields are then partial.
	Unverified error
}

// verifyMediaManifest compares every listed object with the store. It only
// reads. Size and the sha256 recorded in the object's metadata are compared
// (an object without it is read and hashed); the bytes of an object whose
// size and metadata agree are not re-read. A store failure stops the check and is returned in Unverified.
func verifyMediaManifest(ctx context.Context, store media.BlobStore, mm *mediaManifest) mediaCheck {
	res := mediaCheck{Total: len(mm.Files)}
	st, ok := store.(media.Stater)
	if !ok {
		res.Unverified = errors.New("the media store cannot report object sizes")
		return res
	}
	for _, f := range mm.Files {
		info, err := st.Stat(ctx, f.Key)
		if errors.Is(err, fs.ErrNotExist) {
			res.Missing = append(res.Missing, f.Key)
			continue
		}
		if err != nil {
			res.Unverified = err
			return res
		}
		if info.Size != f.Size {
			res.Mismatched = append(res.Mismatched, f.Key)
			continue
		}
		sum := info.SHA256
		if sum == "" {
			if sum, err = hashObject(ctx, store, f.Key); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					res.Missing = append(res.Missing, f.Key)
					continue
				}
				res.Unverified = err
				return res
			}
		}
		if sum != f.SHA256 {
			res.Mismatched = append(res.Mismatched, f.Key)
		}
	}
	return res
}
