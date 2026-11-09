package app

import (
	"context"
	"fmt"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/offsite"
)

// checkMediaOffsiteOverlap refuses a start whose media object storage shares a
// bucket and prefix with the off-site backup target (saved settings or
// HATAMOTO_OFFSITE_*): /media serves every object under the media prefix, so
// the backup archives would be public. It does nothing on local disk.
func checkMediaOffsiteOverlap(ctx context.Context, getenv func(string) string, saved func(context.Context) (offsite.Settings, bool, error)) error {
	if kind, _ := media.StorageFromEnv(getenv); kind != "s3" {
		return nil
	}
	mt, err := media.S3TargetFromEnv(getenv)
	if err != nil {
		return nil // reported by the configuration check
	}
	clash := func(src, endpoint, bucket, prefix string) error {
		if bucket != "" && mt.SharesWith(endpoint, bucket, prefix) {
			return fmt.Errorf("invalid configuration: the media object storage and the off-site backup target (%s) share a bucket and overlapping prefixes; use a separate bucket or distinct prefixes", src)
		}
		return nil
	}
	if ot, err := offsite.TargetFromEnv(getenv); err == nil {
		if err := clash("HATAMOTO_OFFSITE_*", ot.Endpoint, ot.Bucket, ot.Prefix); err != nil {
			return err
		}
	}
	set, found, err := saved(ctx)
	if err != nil {
		return fmt.Errorf("check the off-site target: %w", err)
	}
	if !found || !set.Enabled {
		return nil // a disabled target uploads nothing; enabling it in the admin runs the check
	}
	return clash("saved settings", set.EffectiveEndpoint(), set.Bucket, set.Prefix)
}
