package admin

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

// fakeAuditLog serves rows newest first and records the last window asked for.
type fakeAuditLog struct {
	rows          []AuditRow
	err           error
	limit, offset int
	recentCalls   int // Recent also counts the table; the Activity card must not
	lastQuery     AuditQuery
	facets        AuditFacets
	facetsErr     error
}

// Search filters rows in Go the way the stored reader filters in SQL and
// records the query. Rows carry Source and Result when a test sets them.
func (f *fakeAuditLog) Search(_ context.Context, q AuditQuery) (AuditPage, error) {
	f.lastQuery = q
	if f.err != nil {
		return AuditPage{}, f.err
	}
	needle := strings.ToLower(q.Q)
	var matched []AuditRow
	for _, r := range f.rows {
		src := r.Src
		if src == "" {
			src = auditSrcAdmin
		}
		switch {
		case q.Source != "" && r.Source != q.Source,
			q.Action != "" && r.Action != q.Action,
			q.Actor != "" && !fakeActorMatches(q.Actor, r),
			!q.From.IsZero() && r.At.Before(q.From),
			!q.To.IsZero() && !r.At.Before(q.To),
			q.Before != nil && !fakeOlder(r, src, *q.Before),
			needle != "" && !strings.Contains(strings.ToLower(fmt.Sprint(r.Actor, " ", r.Action, " ", r.Target, " ", r.Detail, " ", r.Error)), needle):
			continue
		}
		matched = append(matched, r)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 25
	}
	if len(matched) <= limit {
		return AuditPage{Rows: matched}, nil
	}
	last := matched[limit-1]
	src := last.Src
	if src == "" {
		src = auditSrcAdmin
	}
	return AuditPage{Rows: matched[:limit], Next: &AuditCursor{At: last.At, Src: src, ID: last.ID}}, nil
}

// fakeActorMatches matches by name for "n:" values; the fake has no ids.
func fakeActorMatches(actor string, r AuditRow) bool {
	name, ok := strings.CutPrefix(actor, "n:")
	return ok && r.Actor == name
}

// fakeOlder reports whether r sorts strictly after the cursor in
// (at, src, id) descending order.
func fakeOlder(r AuditRow, src string, c AuditCursor) bool {
	switch {
	case !r.At.Equal(c.At):
		return r.At.Before(c.At)
	case src != c.Src:
		return src < c.Src
	}
	return r.ID < c.ID
}

func (f *fakeAuditLog) Facets(_ context.Context, _, _ time.Time) (AuditFacets, error) {
	return f.facets, f.facetsErr
}

func (f *fakeAuditLog) Latest(ctx context.Context, limit int) ([]AuditRow, error) {
	rows, _, err := f.Recent(ctx, limit, 0)
	f.recentCalls--
	return rows, err
}

func (f *fakeAuditLog) Recent(_ context.Context, limit, offset int) ([]AuditRow, int, error) {
	f.limit, f.offset = limit, offset
	f.recentCalls++
	if f.err != nil {
		return nil, 0, f.err
	}
	end := min(offset+limit, len(f.rows))
	if offset > end {
		offset = end
	}
	return f.rows[offset:end], len(f.rows), nil
}

func TestAuditLabelNamesEveryActionTheAdminWrites(t *testing.T) {
	t.Parallel()

	for _, a := range []string{"updates.apply_without_backup", "entity.schedule", "plugin.mail_quota"} {
		if l := auditLabel(a); l == a || strings.Contains(l, ".") {
			t.Errorf("%s has no plain label (%q)", a, l)
		}
	}
}

func auditRows(n int) []AuditRow {
	at := time.Date(2026, 10, 2, 8, 30, 0, 0, time.UTC)
	rows := make([]AuditRow, n)
	for i := range rows {
		rows[i] = AuditRow{ID: int64(n - i), At: at.Add(-time.Duration(i) * time.Minute), Actor: "owner@example.com", Action: "entity.publish", Target: fmt.Sprintf("id-%d", i), Detail: map[string]any{"name": fmt.Sprintf("Page %d", i)}}
	}
	return rows
}

func TestAuditLogOwnerSeesPagedRows(t *testing.T) {
	t.Parallel()

	f := &fakeAuditLog{rows: auditRows(30)}
	g := newRig(t, func(d *Deps) { d.AuditLog = f })
	rec := g.do("GET", "/admin/audit", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || f.limit != 25 || f.offset != 0 {
		t.Fatalf("page 1: %d limit=%d offset=%d", rec.Code, f.limit, f.offset)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	for _, want := range []string{"Audit log", "owner@example.com", "Published", "Page 0", "2 Oct 2026 08:30", `href="/admin/audit?page=2"`, "Showing 1–25 of 30"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "Page 25") {
		t.Error("page 1 shows row 26")
	}
	g.do("GET", "/admin/audit?page=2", "owner-token", nil, nil)
	if f.offset != 25 {
		t.Errorf("page 2 offset = %d", f.offset)
	}
}

func TestAuditLogDeniedAndUnmounted(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.AuditLog = &fakeAuditLog{rows: auditRows(1)} })
	if rec := g.do("GET", "/admin/audit", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor = %d", rec.Code)
	}
	if rec := g.do("GET", "/admin/audit", "", nil, nil); rec.Code == 200 {
		t.Errorf("anonymous = %d", rec.Code)
	}
	none := newRig(t)
	if rec := none.do("GET", "/admin/audit", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("no reader = %d", rec.Code)
	}
	if body := none.do("GET", "/admin", "owner-token", nil, nil).Body.String(); strings.Contains(body, "/admin/audit") {
		t.Error("audit link shown without a reader")
	}
}

func TestAuditLogReaderFailure(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.AuditLog = &fakeAuditLog{err: context.DeadlineExceeded} })
	rec := g.do("GET", "/admin/audit", "owner-token", nil, nil)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "could not be loaded") {
		t.Errorf("reader error = %d", rec.Code)
	}
}

func TestAuditLogLabels(t *testing.T) {
	t.Parallel()

	if auditLabel("entity.restore_version") != "Restored a version" || auditLabel("package.install") != "package.install" {
		t.Error("label map or raw fallback wrong")
	}
	if auditTarget(AuditRow{Target: "abc"}) != "abc" || auditTarget(AuditRow{Target: "abc", Detail: map[string]any{"name": "Home"}}) != "Home" {
		t.Error("target should prefer the recorded name")
	}
}

func TestAuditLogGoldens(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.AuditLog = &fakeAuditLog{rows: auditRows(3)} })
	compareGolden(t, "pages/audit-log.html", normalizePage(g.do("GET", "/admin/audit", "owner-token", nil, nil).Body.String()))
	e := newRig(t, func(d *Deps) { d.AuditLog = &fakeAuditLog{} })
	compareGolden(t, "pages/audit-log-empty.html", normalizePage(e.do("GET", "/admin/audit", "owner-token", nil, nil).Body.String()))
}

func TestAuditLogHonoursAdminPath(t *testing.T) {
	t.Parallel()

	g := newRig(t, func(d *Deps) { d.AdminPath = "/x-test"; d.AuditLog = &fakeAuditLog{rows: auditRows(30)} })
	body := g.do("GET", "/x-test/audit", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `href="/x-test/audit?page=2"`) || strings.Contains(body, `"/admin/`) {
		t.Error("pager must use the configured admin path")
	}
}

func TestAuditLabelNamesTheNewActions(t *testing.T) {
	t.Parallel()

	for _, a := range newAuditActions {
		if l := auditLabel(a); l == "" || l == a {
			t.Errorf("%s has no label (%q)", a, l)
		}
		if _, ok := auditDetailKeys[a]; !ok {
			t.Errorf("%s has no detail allow-list", a)
		}
	}
}
