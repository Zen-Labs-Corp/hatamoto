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
