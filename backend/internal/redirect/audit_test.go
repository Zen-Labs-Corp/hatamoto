package redirect

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type auditCall struct {
	action, target string
	detail         map[string]any
}

type failingStore struct{ *fakeStore }

var errStore = errors.New("store down")

func (failingStore) Ignore404Entry(context.Context, uuid.UUID) error    { return errStore }
func (failingStore) Delete404Entry(context.Context, uuid.UUID) error    { return errStore }
func (failingStore) DeleteAll404Entries(context.Context) (int64, error) { return 0, errStore }
func (failingStore) DeleteRedirect(context.Context, uuid.UUID) error    { return errStore }

func auditedService(t *testing.T, store Store) (*Service, *[]auditCall) {
	t.Helper()
	s := newTestService(t, store, nil)
	var calls []auditCall
	s.SetAudit(func(_ context.Context, action, target string, detail map[string]any) {
		calls = append(calls, auditCall{action, target, detail})
	})
	return s, &calls
}

func TestServiceAuditsRedirectWrites(t *testing.T) {
	ctx := context.Background()
	s, calls := auditedService(t, newFakeStore())
	rd, err := s.CreateRedirect(ctx, &CreateRedirectInput{SourcePath: "/old", TargetURL: "/new?token=abc", StatusCode: 301})
	if err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if c.action != "redirect.created" || c.target != rd.ID.String() || c.detail["source"] != "/old" ||
		c.detail["target"] != "/new" || c.detail["status_code"] != 301 || c.detail["enabled"] != true || len(c.detail) != 4 {
		t.Fatalf("create row = %+v", c)
	}
	if _, err := s.UpdateRedirect(ctx, rd.ID, &UpdateRedirectInput{}); err != nil {
		t.Fatal(err)
	}
	if c := (*calls)[1]; c.action != "redirect.updated" || c.target != rd.ID.String() || len(c.detail) != 4 {
		t.Fatalf("update row = %+v", c)
	}
	if err := s.DeleteRedirect(ctx, rd.ID); err != nil {
		t.Fatal(err)
	}
	if c := (*calls)[2]; c.action != "redirect.deleted" || c.target != rd.ID.String() {
		t.Fatalf("delete row = %+v", c)
	}
	id := uuid.New()
	if err := s.Ignore404Entry(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete404Entry(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteAll404Entries(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"not_found.ignored", "not_found.deleted", "404log.cleared"}
	for i, a := range want {
		if got := (*calls)[3+i]; got.action != a {
			t.Fatalf("row %d = %+v, want %s", 3+i, got, a)
		}
	}
	if c := (*calls)[3]; c.target != id.String() {
		t.Fatalf("ignore target = %q", c.target)
	}
	if c := (*calls)[5]; c.detail["count"] != int64(0) {
		t.Fatalf("cleared detail = %v", c.detail)
	}
	if len(*calls) != 6 {
		t.Fatalf("calls = %d, want 6", len(*calls))
	}
}

func TestServiceAuditsNothingOnFailedRedirectWrite(t *testing.T) {
	ctx := context.Background()
	s, calls := auditedService(t, failingStore{newFakeStore()})
	if _, err := s.CreateRedirect(ctx, &CreateRedirectInput{SourcePath: "/a", TargetURL: "/b", StatusCode: 999}); err == nil {
		t.Fatal("want error")
	}
	if _, err := s.UpdateRedirect(ctx, uuid.New(), &UpdateRedirectInput{}); err == nil {
		t.Fatal("want error")
	}
	if s.DeleteRedirect(ctx, uuid.New()) == nil || s.Ignore404Entry(ctx, uuid.New()) == nil || s.Delete404Entry(ctx, uuid.New()) == nil {
		t.Fatal("want errors")
	}
	if _, err := s.DeleteAll404Entries(ctx); err == nil {
		t.Fatal("want error")
	}
	if len(*calls) != 0 {
		t.Fatalf("calls on failure: %+v", *calls)
	}
}

func TestServiceAuditNilHookIsSafe(t *testing.T) {
	s := newTestService(t, newFakeStore(), nil)
	if _, err := s.CreateRedirect(context.Background(), &CreateRedirectInput{SourcePath: "/a", TargetURL: "/b", StatusCode: 301}); err != nil {
		t.Fatal(err)
	}
}
