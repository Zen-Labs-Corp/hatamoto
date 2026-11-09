package media

import (
	"errors"
	"fmt"
	"strings"

	"hatamoto.dev/core/internal/s3"
)

// Environment variables that choose and describe where media bytes live.
// The secret key is read from the environment only: it is never stored in the
// database, the settings or the configuration struct.
const (
	EnvStorage     = "MEDIA_STORAGE"
	EnvS3Endpoint  = "HATAMOTO_MEDIA_S3_ENDPOINT"
	EnvS3Region    = "HATAMOTO_MEDIA_S3_REGION"
	EnvS3Bucket    = "HATAMOTO_MEDIA_S3_BUCKET"
	EnvS3Prefix    = "HATAMOTO_MEDIA_S3_PREFIX"
	EnvS3AccessKey = "HATAMOTO_MEDIA_S3_ACCESS_KEY"
	EnvS3SecretKey = "HATAMOTO_MEDIA_S3_SECRET_KEY"
)

const awsEndpointFormat = "https://s3.%s.amazonaws.com"

// S3Target names a bucket of S3-compatible object storage. Prefix is placed
// verbatim in front of every media key.
type S3Target struct {
	Endpoint  string
	Region    string
	Bucket    string
	Prefix    string
	AccessKey string
	SecretKey string
}

// StorageFromEnv returns the configured driver, "local" or "s3". An unset
// value means local.
func StorageFromEnv(getenv func(string) string) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(getenv(EnvStorage))); v {
	case "", "local":
		return "local", nil
	case "s3":
		return "s3", nil
	default:
		return "", fmt.Errorf("%s: %q is not supported, use local or s3", EnvStorage, v)
	}
}

// S3TargetFromEnv builds the target from HATAMOTO_MEDIA_S3_*. An empty
// endpoint means AWS S3 in the given region. It does not touch the network.
func S3TargetFromEnv(getenv func(string) string) (S3Target, error) {
	t := S3Target{
		Endpoint:  strings.TrimSpace(getenv(EnvS3Endpoint)),
		Region:    strings.TrimSpace(getenv(EnvS3Region)),
		Bucket:    strings.TrimSpace(getenv(EnvS3Bucket)),
		Prefix:    getenv(EnvS3Prefix),
		AccessKey: strings.TrimSpace(getenv(EnvS3AccessKey)),
		SecretKey: getenv(EnvS3SecretKey),
	}
	if t.Endpoint == "" && t.Region != "" {
		t.Endpoint = fmt.Sprintf(awsEndpointFormat, t.Region)
	}
	var missing []string
	for _, f := range []struct{ name, val string }{
		{EnvS3Region, t.Region}, {EnvS3Bucket, t.Bucket}, {EnvS3AccessKey, t.AccessKey}, {EnvS3SecretKey, t.SecretKey},
	} {
		if f.val == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return S3Target{}, errors.New("media object storage is incomplete, set " + strings.Join(missing, ", "))
	}
	if _, err := s3.ValidateEndpoint(t.Endpoint); err != nil {
		return S3Target{}, fmt.Errorf("%s: %w", EnvS3Endpoint, err)
	}
	if err := validatePrefix(t.Prefix); err != nil {
		return S3Target{}, fmt.Errorf("%s: %w", EnvS3Prefix, err)
	}
	return t, nil
}

// validatePrefix accepts an empty prefix or a "/"-terminated path of
// non-empty segments, so a prefix never matches a sibling that merely starts
// with the same letters and never climbs out with "..".
func validatePrefix(p string) error {
	if p == "" {
		return nil
	}
	if !strings.HasSuffix(p, "/") {
		return errors.New("must be empty or end with a slash")
	}
	if strings.HasPrefix(p, "/") {
		return errors.New("must not start with a slash")
	}
	for _, seg := range strings.Split(strings.TrimSuffix(p, "/"), "/") {
		if seg == "" || seg == ".." {
			return errors.New(`must not contain empty or ".." segments`)
		}
	}
	return nil
}

// SharesWith reports whether the other target (an off-site backup target, say)
// is the same bucket at the same endpoint with a prefix that contains this one
// or is contained in it. /media serves every object under the media prefix, so
// such a bucket would publish the other target's objects.
func (t S3Target) SharesWith(endpoint, bucket, prefix string) bool {
	norm := func(e string) string { return strings.ToLower(strings.TrimRight(strings.TrimSpace(e), "/")) }
	if t.Bucket != bucket || norm(t.Endpoint) != norm(endpoint) {
		return false
	}
	return strings.HasPrefix(t.Prefix, prefix) || strings.HasPrefix(prefix, t.Prefix)
}
