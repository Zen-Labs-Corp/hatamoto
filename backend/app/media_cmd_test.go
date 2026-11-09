package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

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
