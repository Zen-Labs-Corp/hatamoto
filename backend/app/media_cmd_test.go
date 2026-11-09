package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/s3/s3test"
)

func TestParseMediaMigrate(t *testing.T) {
	for args, ok := range map[string]bool{
		"media migrate --to s3":              true,
		"media migrate --to local --dry-run": true,
		"media migrate":                      false,
		"media migrate --to gcs":             false,
		"media migrate --to s3 extra":        false,
		"media":                              false,
	} {
		cmd, _ := parseArgs(strings.Fields(args))
		if (cmd == cmdMedia) != ok {
			t.Errorf("%q: command %v, want media=%v", args, cmd, ok)
		}
	}
}

func mediaMigrateFixture(t *testing.T) (mediaMigratePlan, *s3test.Server) {
	t.Helper()
	dir := t.TempDir()
	local, err := media.NewLocalStore(dir, "/media")
	if err != nil {
		t.Fatal(err)
	}
	fake := s3test.NewServer(t)
	st, err := media.NewS3Store(media.S3Target{Endpoint: fake.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "sites/a/media/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := []*media.Media{{StoragePath: "2026/01/a.png"}, {StoragePath: "2026/01/b.png"}}
	for _, m := range rows {
		if _, err := local.Put(context.Background(), m.StoragePath, strings.NewReader("data-"+m.StoragePath)); err != nil {
			t.Fatal(err)
		}
	}
	return mediaMigratePlan{
		mediaMigrateFlags: mediaMigrateFlags{to: "s3"},
		from:              local, dest: st,
		bucket: s3test.Bucket + "/sites/a/media/", source: dir,
		list: func(context.Context) ([]*media.Media, error) { return rows, nil },
	}, fake
}

func TestMediaMigrateOutput(t *testing.T) {
	p, fake := mediaMigrateFixture(t)
	var out bytes.Buffer
	if err := mediaMigrate(context.Background(), p, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"media migrate: local -> s3\n",
		"  bucket  backups/sites/a/media/\n",
		"  [2/2] ", "  done\n",
		"copied           2   verified sha256\n",
		"skipped          0   already at the destination, same sha256\n",
		"failed           0\n",
		"source kept: " + p.source + "\n",
		"next: set MEDIA_STORAGE=s3 and restart\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if len(fake.Objects()) != 2 {
		t.Fatalf("objects: %d", len(fake.Objects()))
	}

	out.Reset()
	if err := mediaMigrate(context.Background(), p, &out); err != nil || !strings.Contains(out.String(), "skipped          2") {
		t.Fatalf("rerun: %v\n%s", err, out.String())
	}
}

func TestMediaMigrateDryRunAndFailure(t *testing.T) {
	p, fake := mediaMigrateFixture(t)
	p.dryRun = true
	var out bytes.Buffer
	if err := mediaMigrate(context.Background(), p, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "(dry run)") || !strings.Contains(out.String(), "would copy") || strings.Contains(out.String(), "next:") || len(fake.Objects()) != 0 {
		t.Fatalf("dry run:\n%s", out.String())
	}

	// A row whose file is missing fails the run with a non-zero exit.
	p.dryRun = false
	p.list = func(context.Context) ([]*media.Media, error) {
		return []*media.Media{{StoragePath: "missing.png"}}, nil
	}
	out.Reset()
	err := mediaMigrate(context.Background(), p, &out)
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 1 || !strings.Contains(out.String(), "failed           1") || strings.Contains(out.String(), "next:") {
		t.Fatalf("failure: %v\n%s", err, out.String())
	}
}

// keysetRepo serves rows ordered by id only, as the real keyset query does.
type keysetRepo struct{ rows []*media.Media }

func (r keysetRepo) ListAllAfter(_ context.Context, after uuid.UUID, limit int) ([]*media.Media, error) {
	var out []*media.Media
	for _, m := range r.rows {
		if bytes.Compare(m.ID[:], after[:]) > 0 && len(out) < limit {
			out = append(out, m)
		}
	}
	return out, nil
}

func TestListAllMediaVisitsEveryRowOnce(t *testing.T) {
	var rows []*media.Media
	for i := 1; i <= 1203; i++ { // more than two pages
		rows = append(rows, &media.Media{ID: uuid.UUID{0, byte(i >> 8), byte(i)}})
	}
	got, err := listAllMedia(context.Background(), keysetRepo{rows})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	for _, m := range got {
		seen[m.ID] = true
	}
	if len(got) != len(rows) || len(seen) != len(rows) {
		t.Errorf("got %d rows (%d distinct), want %d", len(got), len(seen), len(rows))
	}
}

func TestParseMediaSweep(t *testing.T) {
	f, err := parseMediaSweepArgs(nil)
	if err != nil || f.apply || f.olderThan != 24*time.Hour {
		t.Fatalf("defaults = %+v, %v", f, err)
	}
	f, err = parseMediaSweepArgs([]string{"--apply", "--older-than", "72h"})
	if err != nil || !f.apply || f.olderThan != 72*time.Hour {
		t.Fatalf("flags = %+v, %v", f, err)
	}
	for _, bad := range []string{"--older-than soon", "--older-than 30m", "--older-than 0s", "extra"} {
		if _, err := parseMediaSweepArgs(strings.Fields(bad)); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for args, ok := range map[string]bool{
		"media sweep":                  true,
		"media sweep --apply":          true,
		"media sweep --older-than 10m": false,
		"media sweep --to s3":          false,
	} {
		cmd, _ := parseArgs(strings.Fields(args))
		if (cmd == cmdMedia) != ok {
			t.Errorf("%q: command %v, want media=%v", args, cmd, ok)
		}
	}
}

func TestMediaSweepDryRunAndApply(t *testing.T) {
	fake := s3test.NewServer(t)
	st, err := media.NewS3Store(media.S3Target{Endpoint: fake.URL, Region: s3test.Region, Bucket: s3test.Bucket,
		Prefix: "sites/a/", AccessKey: s3test.AccessKey, SecretKey: s3test.SecretKey}, "/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	const dir = "2e0b8f2a-0903-4817-a21f-ed8a69a1e6f3/"
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-72 * time.Hour)
	fake.PutAt("sites/a/"+dir+"kept.png", []byte("12345"), nil, old)
	fake.PutAt("sites/a/"+dir+"orphan.png", []byte("123456"), nil, old)
	fake.PutAt("sites/a/"+dir+"new.png", []byte("123"), nil, now.Add(-time.Minute))
	fake.PutAt("sites/offsite/backup", []byte("1234"), nil, old)
	plan := mediaSweepPlan{
		mediaSweepFlags: mediaSweepFlags{olderThan: 24 * time.Hour},
		store:           st, bucket: s3test.Bucket + "/sites/a/", now: now,
		loadKeys: func(context.Context) (map[string]struct{}, error) {
			return map[string]struct{}{dir + "kept.png": {}}, nil
		},
	}

	var out bytes.Buffer
	if err := mediaSweep(context.Background(), plan, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"dry run: 1 orphan objects under backups/sites/a/ older than 24h (6 B):\n",
		"  " + dir + "orphan.png  6 B  2026-02-26T12:00:00Z\n",
		"run with --apply to delete them\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out.String())
		}
	}
	if len(fake.Objects()) != 4 {
		t.Fatalf("a dry run deleted objects: %d left", len(fake.Objects()))
	}

	plan.apply = true
	out.Reset()
	if err := mediaSweep(context.Background(), plan, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "deleted 1 orphan objects (6 B)\n") {
		t.Errorf("apply output:\n%s", out.String())
	}
	left := fake.Objects()
	if _, ok := left["sites/a/"+dir+"orphan.png"]; ok || len(left) != 3 {
		t.Errorf("objects after apply = %d, orphan still there = %v", len(left), ok)
	}
}
