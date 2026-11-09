package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"hatamoto.dev/core/internal/s3/s3test"
)

func migrateFixture(t *testing.T) (BlobStore, *S3Store, *s3test.Server, []string) {
	t.Helper()
	local, err := NewLocalStore(t.TempDir(), "/media")
	if err != nil {
		t.Fatal(err)
	}
	st, fake := newTestS3Store(t, "sites/a/media/")
	keys := []string{"2026/01/a.png", "2026/01/b.png", "2026/02/c.webp"}
	for i, k := range keys {
		if _, err := local.Put(context.Background(), k, bytes.NewReader(bytes.Repeat([]byte{byte('a' + i)}, 100+i))); err != nil {
			t.Fatal(err)
		}
	}
	return local, st, fake, keys
}

func TestMigrateLocalToS3AndBack(t *testing.T) {
	ctx := context.Background()
	local, st, _, keys := migrateFixture(t)

	var last int
	rep, err := Migrate(ctx, MigrateOptions{From: local, To: st, Keys: keys,
		Progress: func(done, total int, _ int64) { last = done; _ = total }})
	if err != nil || rep.Copied != 3 || rep.Failed != 0 || rep.Skipped != 0 || last != 3 {
		t.Fatalf("copy: %+v %v last=%d", rep, err, last)
	}
	rep, _ = Migrate(ctx, MigrateOptions{From: local, To: st, Keys: keys})
	if rep.Copied != 0 || rep.Skipped != 3 {
		t.Fatalf("rerun: %+v", rep)
	}

	back, _ := NewLocalStore(t.TempDir(), "/media")
	rep, _ = Migrate(ctx, MigrateOptions{From: st, To: back, Keys: keys})
	if rep.Copied != 3 || rep.Failed != 0 {
		t.Fatalf("back: %+v", rep)
	}
	r, _ := back.Get(ctx, keys[1])
	got, _ := io.ReadAll(r)
	r.Close()
	if !bytes.Equal(got, bytes.Repeat([]byte{'b'}, 101)) {
		t.Fatal("bytes differ after round trip")
	}
	// The source is never touched.
	if _, err := local.Get(ctx, keys[0]); err != nil {
		t.Fatalf("source lost: %v", err)
	}
}

func TestMigrateRecopiesCorruptDestination(t *testing.T) {
	ctx := context.Background()
	local, st, fake, keys := migrateFixture(t)
	Migrate(ctx, MigrateOptions{From: local, To: st, Keys: keys})
	fake.Corrupt("sites/a/media/" + keys[2])
	rep, _ := Migrate(ctx, MigrateOptions{From: local, To: st, Keys: keys})
	if rep.Copied != 1 || rep.Skipped != 2 {
		t.Fatalf("report: %+v", rep)
	}
}

func TestMigrateDryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	local, st, fake, keys := migrateFixture(t)
	rep, _ := Migrate(ctx, MigrateOptions{From: local, To: st, Keys: keys, DryRun: true})
	if rep.Copied != 3 || rep.Bytes != 100+101+102 {
		t.Fatalf("report: %+v", rep)
	}
	if n := len(fake.Objects()); n != 0 {
		t.Fatalf("%d objects written", n)
	}
}

func TestMigrateResumesAfterCancel(t *testing.T) {
	local, st, _, keys := migrateFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	rep, err := Migrate(ctx, MigrateOptions{From: local, To: st, Keys: keys,
		Progress: func(done, _ int, _ int64) {
			if done == 1 {
				cancel()
			}
		}})
	if !errors.Is(err, context.Canceled) || rep.Copied != 1 {
		t.Fatalf("interrupted: %+v %v", rep, err)
	}
	rep, err = Migrate(context.Background(), MigrateOptions{From: local, To: st, Keys: keys})
	if err != nil || rep.Copied != 2 || rep.Skipped != 1 {
		t.Fatalf("resume: %+v %v", rep, err)
	}
}

func TestMigrateMissingSourceFails(t *testing.T) {
	local, st, _, _ := migrateFixture(t)
	rep, err := Migrate(context.Background(), MigrateOptions{From: local, To: st, Keys: []string{"nope.png"}})
	if err != nil || rep.Failed != 1 || len(rep.Errors) != 1 {
		t.Fatalf("report: %+v %v", rep, err)
	}
}

func TestMediaKeys(t *testing.T) {
	rows := []*Media{
		{StoragePath: "b.png", Variants: []ImageVariant{{Path: "b.webp"}, {Path: ""}}},
		{StoragePath: "a.png", Variants: []ImageVariant{{Path: "b.webp"}}},
	}
	got := MediaKeys(rows)
	if len(got) != 3 || got[0] != "a.png" || got[1] != "b.png" || got[2] != "b.webp" {
		t.Fatalf("keys: %v", got)
	}
}
