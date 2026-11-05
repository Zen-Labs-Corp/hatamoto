package offsite

import (
	"errors"
	"fmt"
	"strings"
)

// Environment variables that name a target for the command line, which runs on
// a machine with no database and so never reads the saved settings.
const (
	EnvEndpoint  = "HATAMOTO_OFFSITE_ENDPOINT"
	EnvRegion    = "HATAMOTO_OFFSITE_REGION"
	EnvBucket    = "HATAMOTO_OFFSITE_BUCKET"
	EnvPrefix    = "HATAMOTO_OFFSITE_PREFIX"
	EnvAccessKey = "HATAMOTO_OFFSITE_ACCESS_KEY"
	EnvSecretKey = "HATAMOTO_OFFSITE_SECRET_KEY"
)

// TargetFromEnv builds a Target from HATAMOTO_OFFSITE_*. An empty endpoint
// means AWS S3 in the given region.
func TargetFromEnv(getenv func(string) string) (Target, error) {
	t := Target{
		Endpoint:  strings.TrimSpace(getenv(EnvEndpoint)),
		Region:    strings.TrimSpace(getenv(EnvRegion)),
		Bucket:    strings.TrimSpace(getenv(EnvBucket)),
		Prefix:    getenv(EnvPrefix),
		AccessKey: strings.TrimSpace(getenv(EnvAccessKey)),
		SecretKey: getenv(EnvSecretKey),
	}
	if t.Endpoint == "" && t.Region != "" {
		t.Endpoint = fmt.Sprintf(awsEndpointFm, t.Region)
	}
	var missing []string
	for _, f := range []struct{ name, val string }{
		{EnvRegion, t.Region}, {EnvBucket, t.Bucket}, {EnvAccessKey, t.AccessKey}, {EnvSecretKey, t.SecretKey},
	} {
		if f.val == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return Target{}, errors.New("off-site target is incomplete, set " + strings.Join(missing, ", "))
	}
	if err := t.Validate(); err != nil {
		return Target{}, err
	}
	return t, nil
}
