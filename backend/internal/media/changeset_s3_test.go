package media

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestS3StageAttachSweep(t *testing.T) {
	ctx := context.Background()
	e := newS3Env(t)
	run := uuid.New()
	data := pngBytes(t, 1200, 900)

	key, err := e.svc.StageBlob(ctx, run, bytes.NewReader(data), "pic.png")
	if err != nil {
		t.Fatal(err)
	}
	if !e.hasObject(key) {
		t.Fatalf("staged blob not in the bucket: %v", e.fake.Objects())
	}
	e.assertClean(t)

	keys, err := e.svc.ListStagedKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0] != key {
		t.Fatalf("ListStagedKeys = %v, %v", keys, err)
	}
	mt, err := e.svc.StagedKeyModTime(key)
	if err != nil || mt.IsZero() || mt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("StagedKeyModTime = %v, %v", mt, err)
	}

	f, size, err := e.svc.OpenStaged(key)
	if err != nil || size != int64(len(data)) {
		t.Fatalf("OpenStaged = %d, %v", size, err)
	}
	got := make([]byte, 8)
	if _, err := f.ReadAt(got, 0); err != nil || !bytes.Equal(got, data[:8]) {
		t.Errorf("ReadAt = %x, %v", got, err)
	}
	f.Close()
	e.assertClean(t) // the spool of OpenStaged is unlinked at once

	m, err := e.svc.prepareAttach(ctx, MediaOp{StagedKey: key, Filename: "pic.png", Origin: "plug"})
	if err != nil {
		t.Fatal(err)
	}
	if isStagedKey(m.StoragePath) || !e.hasObject(m.StoragePath) || len(m.Variants) == 0 {
		t.Errorf("attached record = %+v", m)
	}
	for _, v := range m.Variants {
		if !e.hasObject(v.Path) {
			t.Errorf("variant %s not in the bucket", v.Path)
		}
	}
	e.assertClean(t)

	if err := e.svc.DeleteStaged(ctx, []string{key}); err != nil {
		t.Fatal(err)
	}
	if keys, _ := e.svc.ListStagedKeys(ctx); len(keys) != 0 {
		t.Errorf("staged keys after sweep: %v", keys)
	}
	e.svc.RemoveFiles(ctx, m)
	if len(e.fake.Objects()) != 0 {
		t.Errorf("objects left: %v", e.fake.Objects())
	}

	// A refused file never reaches the bucket.
	if _, err := e.svc.StageBlob(ctx, run, io.LimitReader(bytes.NewReader([]byte("<html>")), 6), "x.png"); err == nil {
		t.Error("expected a type error")
	}
	if len(e.fake.Objects()) != 0 {
		t.Errorf("refused file stored: %v", e.fake.Objects())
	}
	e.assertClean(t)
}
