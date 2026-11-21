package staticexport

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"hatamoto.dev/core/internal/offsite"
	"hatamoto.dev/core/internal/secretbox"
)

// Hosting modes and targets.
const (
	ModeServed = "served"
	ModeStatic = "static"
	TargetDir  = "dir"
	TargetS3   = "s3"

	secretDomain  = "hatamoto/static/export-secret-key"
	secretAAD     = "static-hosting-settings:secret-key"
	awsEndpointFm = "https://s3.%s.amazonaws.com"
)

// Environment variables that name an S3 target for the command line.
const (
	EnvEndpoint  = "HATAMOTO_STATIC_S3_ENDPOINT"
	EnvRegion    = "HATAMOTO_STATIC_S3_REGION"
	EnvBucket    = "HATAMOTO_STATIC_S3_BUCKET"
	EnvPrefix    = "HATAMOTO_STATIC_S3_PREFIX"
	EnvAccessKey = "HATAMOTO_STATIC_S3_ACCESS_KEY"
	EnvSecretKey = "HATAMOTO_STATIC_S3_SECRET_KEY"
)

// Settings is the saved static hosting configuration. No row means the site
// is served and nothing exports.
type Settings struct {
	Mode         string
	Target       string
	DirPath      string
	Service      string
	Endpoint     string // ignored for AWS
	Region       string
	Bucket       string
	Prefix       string
	AccessKey    string
	SecretSealed []byte // sealed secret key; nil when none is saved
	LiveOrigin   string
	UpdatedAt    time.Time // set by the database
}

// Defaults are the settings of a site that never configured static hosting.
func Defaults() Settings {
	return Settings{Mode: ModeServed, Target: TargetDir, Service: offsite.ServiceAWS}
}

// EffectiveEndpoint is the endpoint requests go to: derived from the region
// for AWS, the saved endpoint otherwise.
func (s Settings) EffectiveEndpoint() string {
	if s.Service == offsite.ServiceAWS {
		return fmt.Sprintf(awsEndpointFm, s.Region)
	}
	return s.Endpoint
}

// HasSecret reports whether a sealed secret key is stored.
func (s Settings) HasSecret() bool { return len(s.SecretSealed) > 0 }

// Validate checks the fields against their allowed values. The S3 fields are
// checked only when static mode publishes to S3.
func (s Settings) Validate() error {
	if s.Mode != ModeServed && s.Mode != ModeStatic {
		return fmt.Errorf("unknown mode %q", s.Mode)
	}
	if s.Target != TargetDir && s.Target != TargetS3 {
		return fmt.Errorf("unknown target %q", s.Target)
	}
	if s.DirPath != "" && !filepath.IsAbs(s.DirPath) {
		return errors.New("directory must be an absolute path")
	}
	if s.Mode != ModeStatic {
		return nil
	}
	switch s.Target {
	case TargetDir:
		if s.DirPath == "" {
			return errors.New("directory is required")
		}
	case TargetS3:
		known := false
		for _, v := range offsite.Services {
			known = known || v == s.Service
		}
		if !known {
			return fmt.Errorf("unknown service %q", s.Service)
		}
		if !s.HasSecret() {
			return errors.New("secret key is required")
		}
		// The secret is checked for presence above; Validate needs a value.
		t := offsite.Target{Endpoint: s.EffectiveEndpoint(), Region: s.Region, Bucket: s.Bucket,
			Prefix: s.Prefix, AccessKey: s.AccessKey, SecretKey: "-"}
		if err := t.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// SealSecret returns the sealed form of a secret key for Settings.SecretSealed.
func SealSecret(key []byte, secret string) ([]byte, error) {
	return secretbox.Seal(key, []byte(secretAAD), []byte(secret))
}

// Secret opens the stored secret key. ok is false when none is saved or it
// cannot be read (the server's secret changed).
func (s Settings) Secret(key []byte) (secret string, ok bool) {
	if len(s.SecretSealed) == 0 {
		return "", false
	}
	b, err := secretbox.Open(key, []byte(secretAAD), s.SecretSealed)
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// SecretKey derives the key that seals the secret key from the server secret.
func SecretKey(serverSecret string) []byte { return secretbox.DeriveKey(serverSecret, secretDomain) }

// S3Target returns the connection details with the opened secret key. The
// error never contains the secret.
func (s Settings) S3Target(key []byte) (offsite.Target, error) {
	secret, ok := s.Secret(key)
	if !ok {
		if s.HasSecret() {
			return offsite.Target{}, errors.New("S3 secret key cannot be opened: was HATAMOTO_SECRET changed?")
		}
		return offsite.Target{}, errors.New("S3 secret key is not saved")
	}
	t := offsite.Target{Endpoint: s.EffectiveEndpoint(), Region: s.Region, Bucket: s.Bucket,
		Prefix: s.Prefix, AccessKey: s.AccessKey, SecretKey: secret}
	if err := t.Validate(); err != nil {
		return offsite.Target{}, err
	}
	return t, nil
}

// S3TargetFromEnv builds a target from HATAMOTO_STATIC_S3_*. ok is false when
// EnvBucket is not set (the environment names no target). An empty endpoint
// means AWS S3 in the given region.
func S3TargetFromEnv(getenv func(string) string) (t offsite.Target, ok bool, err error) {
	bucket := strings.TrimSpace(getenv(EnvBucket))
	if bucket == "" {
		return offsite.Target{}, false, nil
	}
	t = offsite.Target{
		Endpoint:  strings.TrimSpace(getenv(EnvEndpoint)),
		Region:    strings.TrimSpace(getenv(EnvRegion)),
		Bucket:    bucket,
		Prefix:    getenv(EnvPrefix),
		AccessKey: strings.TrimSpace(getenv(EnvAccessKey)),
		SecretKey: getenv(EnvSecretKey),
	}
	if t.Endpoint == "" && t.Region != "" {
		t.Endpoint = fmt.Sprintf(awsEndpointFm, t.Region)
	}
	var missing []string
	for _, f := range []struct{ name, val string }{
		{EnvRegion, t.Region}, {EnvAccessKey, t.AccessKey}, {EnvSecretKey, t.SecretKey},
	} {
		if f.val == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return offsite.Target{}, true, errors.New("S3 target is incomplete, set " + strings.Join(missing, ", "))
	}
	if err := t.Validate(); err != nil {
		return offsite.Target{}, true, err
	}
	return t, true, nil
}
