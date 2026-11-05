package admin

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// fakeUnpublish keeps scheduled unpublishes in memory.
type fakeUnpublish struct {
	mu   sync.Mutex
	rows map[uuid.UUID]entity.Unpublish
}

func newFakeUnpublish() *fakeUnpublish { return &fakeUnpublish{rows: map[uuid.UUID]entity.Unpublish{}} }

func (f *fakeUnpublish) SetUnpublish(_ context.Context, id uuid.UUID, at time.Time, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[id] = entity.Unpublish{EntityID: id, At: at.UTC(), RedirectTarget: target}
	return nil
}

func (f *fakeUnpublish) ClearUnpublish(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, id)
	return nil
}

func (f *fakeUnpublish) UnpublishFor(_ context.Context, id uuid.UUID) (*entity.Unpublish, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.rows[id]; ok {
		return &u, nil
	}
	return nil, nil
}

func (f *fakeUnpublish) UnpublishMap(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]entity.Unpublish, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[uuid.UUID]entity.Unpublish{}
	for _, id := range ids {
		if u, ok := f.rows[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}

func TestUnpublishFieldErrors(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	id := fid(12).String()
	future := func(d time.Duration) string { return time.Now().UTC().Add(d).Format(scheduleLayout) }
	post := func(extra url.Values) (int, string) {
		form := url.Values{"name": {"Beta service"}, "slug": {"beta"}, "status": {"draft"}, "attr.summary": {"The second"}}
		for k, v := range extra {
			form[k] = v
		}
		rec := c.post("/admin/entities/"+id, form)
		return rec.Code, rec.Body.String()
	}
	for name, tc := range map[string]struct {
		form url.Values
		want string
	}{
		"past":             {url.Values{"unpublish_at": {future(-48 * time.Hour)}}, "Pick a time in the future"},
		"before publish":   {url.Values{"status": {"scheduled"}, "schedule": {future(72 * time.Hour)}, "unpublish_at": {future(48 * time.Hour)}}, "Pick a time after the publish time"},
		"invalid redirect": {url.Values{"unpublish_at": {future(48 * time.Hour)}, "unpublish_redirect": {"javascript:alert(1)"}}, "Use a path starting with /"},
		"redirect only":    {url.Values{"unpublish_at": {""}, "unpublish_redirect": {"/news"}}, "Pick the date and time to unpublish first"},
		"not a date":       {url.Values{"unpublish_at": {"soon"}}, "Enter a valid date and time"},
	} {
		code, body := post(tc.form)
		if code != 422 || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d, want 422 with %q", name, code, tc.want)
		}
	}
	if len(c.unpub.rows) != 0 {
		t.Error("a schedule was stored although validation refused it")
	}
}

func TestUnpublishSetClearAndAudit(t *testing.T) {
	t.Parallel()

	c := newContentRig(t)
	var actions []string
	c.rig.a.deps.Audit = func(_ context.Context, e AuditEvent) { actions = append(actions, e.Action) }
	id := fid(11)
	at := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Minute)
	form := url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {"The first"},
		"unpublish_at": {at.Format(scheduleLayout)}, "unpublish_redirect": {"/news"}}
	if rec := c.post("/admin/entities/"+id.String(), form); rec.Code != 303 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	u, _ := c.unpub.UnpublishFor(context.Background(), id)
	if u == nil || !u.At.Equal(at) || u.RedirectTarget != "/news" {
		t.Fatalf("stored = %+v, want %v /news", u, at)
	}
	if !strings.Contains(strings.Join(actions, ","), auditEntityUnpublishSchedule) {
		t.Errorf("audit = %v, want %s", actions, auditEntityUnpublishSchedule)
	}
	body := c.get(t, "owner-token", "/admin/entities/"+id.String(), "")
	for _, want := range []string{`name="unpublish_at"`, "Unpublish at", "send visitors to", `name="unpublish_clear"`} {
		if !strings.Contains(body, want) {
			t.Errorf("edit page lacks %q", want)
		}
	}
	if list := c.get(t, "owner-token", "/admin/entities", ""); !strings.Contains(list, "Published, ends "+at.Format("2 Jan")) {
		t.Error("list does not show when the entry ends")
	}

	// Saving the same values again does not write another audit row.
	actions = nil
	c.post("/admin/entities/"+id.String(), form)
	if len(actions) != 0 && strings.Contains(strings.Join(actions, ","), "unpublish_schedule") {
		t.Errorf("unchanged schedule audited again: %v", actions)
	}

	form.Set("unpublish_clear", "true")
	if rec := c.post("/admin/entities/"+id.String(), form); rec.Code != 303 {
		t.Fatalf("clear: %d", rec.Code)
	}
	if u, _ := c.unpub.UnpublishFor(context.Background(), id); u != nil {
		t.Errorf("schedule survived Clear: %+v", u)
	}
	if !strings.Contains(strings.Join(actions, ","), auditEntityUnpublishClear) {
		t.Errorf("audit = %v, want %s", actions, auditEntityUnpublishClear)
	}

	// A form without the field leaves a schedule alone.
	_ = c.unpub.SetUnpublish(context.Background(), id, at, "")
	c.post("/admin/entities/"+id.String(), url.Values{"name": {"Alpha service"}, "slug": {"alpha"}, "status": {"published"}, "attr.summary": {"The first"}})
	if u, _ := c.unpub.UnpublishFor(context.Background(), id); u == nil {
		t.Error("a form without the field removed the schedule")
	}
}
