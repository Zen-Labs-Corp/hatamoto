package staticexport

import (
	"errors"
	"fmt"
	"strings"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/offsite"
	"hatamoto.dev/core/internal/safefetch"
)

// SavedTarget builds the publish target from saved settings: the directory
// through NewDirTarget (guard g), S3 or SFTP with the sealed secret opened by key.
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
	case TargetSFTP:
		cfg, err := set.SFTPConfig(key, getenv)
		if err != nil {
			return nil, err
		}
		if !cfg.AllowPrivate {
			if err := CheckSFTPHost(cfg.Host); err != nil {
				return nil, err
			}
		}
		return NewSFTPTarget(cfg)
	}
	return nil, fmt.Errorf("unknown target %q", set.Target)
}

// S3TargetChecked builds the S3 target and refuses one that overlaps the
// media object storage (/media serves everything under the media prefix). Unless
// EnvS3AllowPrivate is 1 it refuses a private endpoint: IP literals here, and
// hostnames at dial time (the HTTP client resolves and checks every connection).
func S3TargetChecked(t offsite.Target, getenv func(string) string) (Target, error) {
	return s3TargetChecked(t, getenv, nil)
}

// s3TargetChecked is S3TargetChecked with an injectable lookup (nil: system resolver).
func s3TargetChecked(t offsite.Target, getenv func(string) string, lookup safefetch.Lookup) (Target, error) {
	allow := AllowPrivateS3(getenv)
	if !allow {
		if err := CheckS3Endpoint(t.Endpoint); err != nil {
			return nil, fmt.Errorf("%w (set %s=1 to allow a private storage service)", err, EnvS3AllowPrivate)
		}
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if kind, _ := media.StorageFromEnv(getenv); kind == "s3" {
		if mt, err := media.S3TargetFromEnv(getenv); err == nil && mt.Overlaps(t.Endpoint, t.Bucket, t.Prefix) {
			return nil, errors.New("the static export target and the media object storage share a bucket and overlapping prefixes; use a separate bucket or distinct prefixes")
		}
	}
	prefix := t.Prefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	c, err := offsite.Target{Endpoint: t.Endpoint, Region: t.Region, Bucket: t.Bucket, AccessKey: t.AccessKey, SecretKey: t.SecretKey}.Client(s3HTTPClient(allow, lookup))
	if err != nil {
		return nil, err
	}
	return NewS3Target(c, t.Endpoint, t.Bucket, prefix), nil
}
