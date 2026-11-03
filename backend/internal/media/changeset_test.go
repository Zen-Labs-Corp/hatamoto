package media

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStageBlobWritesUnderRunPrefixWithoutRow(t *testing.T) {
	svc := newTestService(t, Config{})
	repo := svc.repo.(*fakeRepo)
	run := uuid.New()
	data := pngBytes(t, 40, 30)

	key, err := svc.StageBlob(context.Background(), run, bytes.NewReader(data), "pic.png")
	if err != nil {
		t.Fatal(err)
	}
	if want := stagedPrefix(run) + "/"; !strings.HasPrefix(key, want) {
		t.Fatalf("key %q lacks prefix %q", key, want)
	}
	if repo.created != nil {
		t.Fatal("staging created a database row")
	}
	if _, err := os.Stat(filepath.Join(svc.uploadRoot(), key)); err != nil {
		t.Fatalf("staged file missing: %v", err)
	}
}

func TestStageBlobRefusesWhatUploadRefuses(t *testing.T) {
	svc := newTestService(t, Config{MaxSizeMB: 1})
	run := uuid.New()
	ctx := context.Background()

	if _, err := svc.StageBlob(ctx, run, strings.NewReader("<html><script>x</script></html>"), "page.png"); !errors.Is(err, ErrInvalidMimeType) {
		t.Errorf("html named png: err = %v, want ErrInvalidMimeType", err)
	}
	big := append(pngBytes(t, 10, 10), make([]byte, 2<<20)...)
	if _, err := svc.StageBlob(ctx, run, bytes.NewReader(big), "big.png"); !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("oversize: err = %v, want ErrFileTooLarge", err)
	}
	if keys, _ := svc.ListStagedKeys(ctx); len(keys) != 0 {
		t.Errorf("refused files left behind: %v", keys)
	}
}

func TestStagedFilesIgnoredByQuota(t *testing.T) {
	svc := newTestService(t, Config{})
	data := pngBytes(t, 40, 30)
	before := dirSize(svc.uploadRoot())
	if _, err := svc.StageBlob(context.Background(), uuid.New(), bytes.NewReader(data), "pic.png"); err != nil {
		t.Fatal(err)
	}
	if got := dirSize(svc.uploadRoot()); got != before {
		t.Errorf("dirSize = %d after staging, want %d", got, before)
	}
}

func TestListStagedKeysAndDeleteStaged(t *testing.T) {
	svc := newTestService(t, Config{})
	ctx := context.Background()
	data := pngBytes(t, 40, 30)
	if keys, err := svc.ListStagedKeys(ctx); err != nil || len(keys) != 0 {
		t.Fatalf("empty store: keys=%v err=%v", keys, err)
	}
	a, _ := svc.StageBlob(ctx, uuid.New(), bytes.NewReader(data), "a.png")
	b, _ := svc.StageBlob(ctx, uuid.New(), bytes.NewReader(data), "b.png")
	keys, err := svc.ListStagedKeys(ctx)
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
	if err := svc.DeleteStaged(ctx, []string{a, a, b}); err != nil {
		t.Fatalf("DeleteStaged (repeat and missing ignored): %v", err)
	}
	if keys, _ := svc.ListStagedKeys(ctx); len(keys) != 0 {
		t.Errorf("keys after delete: %v", keys)
	}
}

func TestDeleteStagedRefusesOtherKeys(t *testing.T) {
	svc := newTestService(t, Config{})
	live := filepath.Join(legacyMediaTenantDir, "live.png")
	for _, k := range []string{live, "../outside", "/etc/passwd", legacyMediaTenantDir + "/staged/../live.png"} {
		if err := svc.DeleteStaged(context.Background(), []string{k}); err == nil {
			t.Errorf("DeleteStaged(%q) accepted", k)
		}
	}
}

func TestPrepareAttachCopiesToFinalKey(t *testing.T) {
	svc := newTestService(t, Config{})
	ctx := context.Background()
	data := pngBytes(t, 1200, 900)
	key, err := svc.StageBlob(ctx, uuid.New(), bytes.NewReader(data), "pic.png")
	if err != nil {
		t.Fatal(err)
	}
	alt := "alt"
	m, err := svc.prepareAttach(ctx, MediaOp{StagedKey: key, Filename: "pic.png", Origin: "plug", AltText: &alt})
	if err != nil {
		t.Fatal(err)
	}
	if isStagedKey(m.StoragePath) {
		t.Errorf("attached record still points into staged/: %s", m.StoragePath)
	}
	if m.MimeType != "image/png" || m.FileSize != int64(len(data)) || m.Folder != "/" || len(m.Variants) == 0 {
		t.Errorf("unexpected record: %+v", m)
	}
	if _, err := os.Stat(filepath.Join(svc.uploadRoot(), m.StoragePath)); err != nil {
		t.Errorf("final file missing: %v", err)
	}
	svc.RemoveFiles(ctx, m)
	if _, err := os.Stat(filepath.Join(svc.uploadRoot(), m.StoragePath)); !os.IsNotExist(err) {
		t.Errorf("RemoveFiles left the file: %v", err)
	}
}

func TestPrepareAttachRefusesNonStagedKey(t *testing.T) {
	svc := newTestService(t, Config{})
	for _, k := range []string{filepath.Join(legacyMediaTenantDir, "x.png"), "../x.png"} {
		if _, err := svc.prepareAttach(context.Background(), MediaOp{StagedKey: k, Filename: "x.png"}); err == nil {
			t.Errorf("prepareAttach(%q) accepted", k)
		}
	}
}

func TestStagedKeyModTime(t *testing.T) {
	svc := newTestService(t, Config{})
	key, err := svc.StageBlob(context.Background(), uuid.New(), bytes.NewReader(pngBytes(t, 8, 8)), "pic.png")
	if err != nil {
		t.Fatal(err)
	}
	mt, err := svc.StagedKeyModTime(key)
	if err != nil || time.Since(mt) > time.Minute {
		t.Fatalf("mod time = %v, %v", mt, err)
	}
	if _, err := svc.StagedKeyModTime("media/../etc/passwd"); err == nil {
		t.Error("a key outside the staged directory was accepted")
	}
	if _, err := svc.StagedKeyModTime(legacyMediaTenantDir + "/live.png"); err == nil {
		t.Error("a published file key was accepted")
	}
}
