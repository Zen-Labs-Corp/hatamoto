package app

import (
	"context"
	"testing"

	"hatamoto.dev/core/internal/offsite"
)

func overlapEnv(m map[string]string) func(string) string {
	base := map[string]string{
		"MEDIA_STORAGE": "s3", "HATAMOTO_MEDIA_S3_ENDPOINT": "https://objects.example.com", "HATAMOTO_MEDIA_S3_REGION": "r1",
		"HATAMOTO_MEDIA_S3_BUCKET": "shared", "HATAMOTO_MEDIA_S3_PREFIX": "media/",
		"HATAMOTO_MEDIA_S3_ACCESS_KEY": "ak", "HATAMOTO_MEDIA_S3_SECRET_KEY": "sk",
	}
	for k, v := range m {
		base[k] = v
	}
	return func(k string) string { return base[k] }
}

func TestMediaOffsiteOverlap(t *testing.T) {
	none := func(context.Context) (offsite.Settings, bool, error) { return offsite.Settings{}, false, nil }
	saved := func(bucket, prefix string) func(context.Context) (offsite.Settings, bool, error) {
		return func(context.Context) (offsite.Settings, bool, error) {
			return offsite.Settings{Enabled: true, Service: offsite.ServiceCustom, Endpoint: "https://objects.example.com", Region: "r1", Bucket: bucket, Prefix: prefix}, true, nil
		}
	}
	offEnv := func(prefix string) map[string]string {
		return map[string]string{
			"HATAMOTO_OFFSITE_ENDPOINT": "https://objects.example.com", "HATAMOTO_OFFSITE_REGION": "r1", "HATAMOTO_OFFSITE_BUCKET": "shared",
			"HATAMOTO_OFFSITE_PREFIX": prefix, "HATAMOTO_OFFSITE_ACCESS_KEY": "ak", "HATAMOTO_OFFSITE_SECRET_KEY": "sk",
		}
	}
	for name, tc := range map[string]struct {
		env     map[string]string
		saved   func(context.Context) (offsite.Settings, bool, error)
		refused bool
	}{
		"local disk":             {map[string]string{"MEDIA_STORAGE": ""}, saved("shared", ""), false},
		"no off-site":            {nil, none, false},
		"saved, empty prefix":    {nil, saved("shared", ""), false},
		"saved, distinct prefix": {nil, saved("shared", "backups/"), false},
		"saved, nested prefix":   {nil, saved("shared", "media/x/"), true},
		"saved, other bucket":    {nil, saved("other", ""), false},
		"env, nested prefix":     {offEnv("media/old/"), none, true},
		"env, distinct prefix":   {offEnv("backups/"), none, false},
		"env, empty prefix":      {offEnv(""), none, false},
	} {
		err := checkMediaOffsiteOverlap(context.Background(), overlapEnv(tc.env), tc.saved)
		if (err != nil) != tc.refused {
			t.Errorf("%s: err = %v, want refused=%v", name, err, tc.refused)
		}
	}
}

func TestMediaOffsiteOverlapIgnoresDisabledSaved(t *testing.T) {
	disabled := func(context.Context) (offsite.Settings, bool, error) {
		return offsite.Settings{Endpoint: "https://objects.example.com", Region: "r1", Bucket: "shared", Prefix: "media/"}, true, nil
	}
	if err := checkMediaOffsiteOverlap(context.Background(), overlapEnv(nil), disabled); err != nil {
		t.Errorf("a disabled saved target was refused: %v", err)
	}
}
