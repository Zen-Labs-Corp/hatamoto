package staticexport

import (
	"errors"
	"fmt"
	"strings"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/offsite"
)

// SavedTarget builds the publish target from saved settings: the directory
// through NewDirTarget (guard g), or S3 with the sealed secret opened by key.
// It refuses an S3 target that overlaps the media object storage. Errors
// never contain the secret.
func SavedTarget(set Settings, key []byte, g DirGuard, getenv func(string) string) (Target, error) {
	switch set.Target {
	case TargetDir:
		if set.DirPath == "" {
			return nil, errors.New("no directory saved")
		}
		return NewDirTarget(set.DirPath, g)
	case TargetS3:
		t, err := set.S3Target(key)
		if err != nil {
			return nil, err
		}
		return S3TargetChecked(t, getenv)
	}
	return nil, fmt.Errorf("unknown target %q", set.Target)
}

// S3TargetChecked builds the S3 target and refuses one that overlaps the
// media object storage (/media serves everything under the media prefix).
func S3TargetChecked(t offsite.Target, getenv func(string) string) (Target, error) {
	if kind, _ := media.StorageFromEnv(getenv); kind == "s3" {
		if mt, err := media.S3TargetFromEnv(getenv); err == nil && mt.Overlaps(t.Endpoint, t.Bucket, t.Prefix) {
			return nil, errors.New("the static export target and the media object storage share a bucket and overlapping prefixes; use a separate bucket or distinct prefixes")
		}
	}
	prefix := t.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	c, err := offsite.Target{Endpoint: t.Endpoint, Region: t.Region, Bucket: t.Bucket, AccessKey: t.AccessKey, SecretKey: t.SecretKey}.Client(nil)
	if err != nil {
		return nil, err
	}
	return NewS3Target(c, t.Endpoint, t.Bucket, prefix), nil
}
