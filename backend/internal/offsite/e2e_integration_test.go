//go:build integration

package offsite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hatamoto.dev/core/internal/s3/s3test"
)

// The scheduled path against a real Postgres and the S3 test server: a slot is
// claimed once per day, each claimed run uploads and records its result, and
// the bucket is pruned to the number of copies to keep.
func TestScheduledUploadEndToEnd(t *testing.T) {
	pool := newIntegrationPool(t)
	ctx := context.Background()
	st := NewStore(pool)
	srv := s3test.NewServer(t)
	key := SecretKey("server-secret")
	sealed, err := SealSecret(key, s3test.SecretKey)
	if err != nil {
		t.Fatal(err)
	}
	set := Settings{
		Enabled: true, Service: ServiceCustom, Endpoint: srv.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "site/", AccessKey: s3test.AccessKey, SecretSealed: sealed,
		Schedule: Schedule{Kind: ScheduleDaily, Hour: 3}, Keep: 2,
		NextDueAt: time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC),
	}
	if err := st.Save(ctx, set); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ran := 0
	for day := 1; day <= 4; day++ {
		now := time.Date(2026, 11, day, 3, 0, 30, 0, time.UTC)
		for range 2 { // two ticks on one day: one claim
			got, _, err := st.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ok, err := st.Claim(ctx, now, got.Schedule.Next(now))
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				continue
			}
			ran++
			target, found := got.Target(key)
			if !found {
				t.Fatal("no target")
			}
			c, err := target.Client(nil)
			if err != nil {
				t.Fatal(err)
			}
			id := idN(day)
			path := filepath.Join(dir, id+".tar")
			if err := os.WriteFile(path, []byte("archive "+id), 0o600); err != nil {
				t.Fatal(err)
			}
			res, err := Upload(ctx, c, got.Prefix, id, path, got.Keep)
			if err != nil || !res.OK {
				t.Fatalf("upload day %d: %+v, %v", day, res, err)
			}
			if err := st.SaveResult(ctx, res); err != nil {
				t.Fatal(err)
			}
		}
	}
	if ran != 4 {
		t.Fatalf("%d runs for 4 days", ran)
	}
	objs := srv.Objects()
	if _, ok := objs["site/"+idN(1)+".tar"]; len(objs) != 2 || ok {
		t.Fatalf("bucket after prune to 2 holds %d objects", len(objs))
	}
	got, _, _ := st.Get(ctx)
	if !got.Last.OK || got.Last.SHA256 == "" {
		t.Fatalf("last = %+v", got.Last)
	}
}
