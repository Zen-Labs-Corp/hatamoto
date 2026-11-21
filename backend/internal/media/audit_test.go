package media

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
)

type auditCall struct {
	action, target string
	detail         map[string]any
}

func auditedService(t *testing.T) (*Service, *[]auditCall) {
	t.Helper()
	svc := newTestService(t, Config{})
	var calls []auditCall
	svc.SetAudit(func(_ context.Context, action, target string, detail map[string]any) {
		calls = append(calls, auditCall{action, target, detail})
	})
	return svc, &calls
}

func TestServiceAuditsMediaWrites(t *testing.T) {
	ctx := context.Background()
	svc, calls := auditedService(t)
	data := pngBytes(t, 20, 20)
	m, err := svc.Upload(ctx, nil, bytes.NewReader(data), "pic.png", "", int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("upload calls = %+v", *calls)
	}
	up := (*calls)[0]
	if up.action != "media.uploaded" || up.target != m.ID.String() || up.detail["name"] != "pic.png" ||
		up.detail["mime"] != "image/png" || up.detail["size"] != m.FileSize || up.detail["folder"] != "/" || len(up.detail) != 4 {
		t.Fatalf("upload row = %+v", up)
	}

	alt := "alt"
	if _, err := svc.Update(ctx, m.ID, &UpdateMediaInput{AltText: &alt}); err != nil {
		t.Fatal(err)
	}
	upd := (*calls)[1]
	if upd.action != "media.updated" || upd.target != m.ID.String() || len(upd.detail) != 1 {
		t.Fatalf("update row = %+v", upd)
	}
	if ch, _ := upd.detail["changed"].([]string); len(ch) != 1 || ch[0] != "alt_text" {
		t.Fatalf("changed = %#v", upd.detail["changed"])
	}

	if err := svc.Delete(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	del := (*calls)[2]
	if del.action != "media.deleted" || del.target != m.ID.String() || del.detail["name"] != "pic.png" || len(*calls) != 3 {
		t.Fatalf("delete row = %+v (calls %d)", del, len(*calls))
	}
}

func TestServiceAuditsNothingOnFailedMediaWrite(t *testing.T) {
	ctx := context.Background()
	svc, calls := auditedService(t)
	if _, err := svc.Upload(ctx, nil, bytes.NewReader([]byte("<html>")), "x.png", "", 6); err == nil {
		t.Fatal("want upload error")
	}
	alt := "a"
	if _, err := svc.Update(ctx, uuid.New(), &UpdateMediaInput{AltText: &alt}); err == nil {
		t.Fatal("want update error")
	}
	if err := svc.Delete(ctx, uuid.New()); err == nil {
		t.Fatal("want delete error")
	}
	if len(*calls) != 0 {
		t.Fatalf("calls on failure: %+v", *calls)
	}
}

func TestServiceAuditNilHookIsSafe(t *testing.T) {
	svc := newTestService(t, Config{})
	data := pngBytes(t, 8, 8)
	if _, err := svc.Upload(context.Background(), nil, bytes.NewReader(data), "p.png", "", int64(len(data))); err != nil {
		t.Fatal(err)
	}
}
