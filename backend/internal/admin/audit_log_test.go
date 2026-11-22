package admin

import (
	"context"
	"fmt"
	"html"
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
	facetFrom     time.Time
	facetTo       time.Time
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

func (f *fakeAuditLog) Facets(_ context.Context, from, to time.Time) (AuditFacets, error) {
	f.facetFrom, f.facetTo = from, to
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

	for _, a := range []string{"updates.apply_without_backup", "entity.schedule", "plugin.mail_quota", "audit.retention"} {
		if l := auditLabel(a); l == a || strings.Contains(l, ".") {
			t.Errorf("%s has no plain label (%q)", a, l)
		}
	}
}

func TestAuditLabelsOfTheReviewFlow(t *testing.T) {
	t.Parallel()

	for action, want := range map[string]string{
		"changeset.edit_submit":    "Sent for review",
		"changeset.stage_decision": "Review decision",
		"changeset.edit_publish":   "Published a reviewed change",
		"changeset.edit_discard":   "Withdrew or discarded a change waiting for review",
		"settings.review":          "Turned review on or off",
		"changeset.publish":        "Published plugin changes", // unchanged
	} {
		if got := auditLabel(action); got != want {
			t.Errorf("auditLabel(%q) = %q, want %q", action, got, want)
		}
	}
}

func auditRows(n int) []AuditRow {
	at := time.Date(2026, 10, 2, 8, 30, 0, 0, time.UTC)
	rows := make([]AuditRow, n)
	for i := range rows {
		rows[i] = AuditRow{ID: int64(n - i), At: at.Add(-time.Duration(i) * time.Minute), Actor: "owner@example.com", Action: "entity.publish", Target: fmt.Sprintf("id-%d", i), Src: auditSrcAdmin, Source: "admin", Result: "ok", Detail: map[string]any{"name": fmt.Sprintf("Page %d", i)}}
	}
	return rows
}

func TestAuditLogOwnerSeesFirstPageAndOlderLink(t *testing.T) {
	t.Parallel()

	f := &fakeAuditLog{rows: auditRows(30)}
	g := newRig(t, func(d *Deps) { d.AuditLog = f })
	rec := g.do("GET", "/admin/audit", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 || f.lastQuery.Limit != 25 || f.lastQuery.Before != nil {
		t.Fatalf("page 1: %d query=%+v", rec.Code, f.lastQuery)
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
	for _, want := range []string{"Audit log", "owner@example.com", "Published", "Page 0", "2 Oct 2026 08:30", ">Source<", ">Result<", ">Admin<", ">OK<", `id="audit-log-older"`, `/admin/audit?before=`, `id="row-a30"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "Page 25") {
		t.Error("page 1 shows row 26")
	}
	older := f.lastPageNext(t, g)
	rec = g.do("GET", older, "owner-token", nil, nil)
	if rec.Code != 200 || f.lastQuery.Before == nil || !strings.Contains(rec.Body.String(), "Page 25") || strings.Contains(rec.Body.String(), "Page 0<") {
		t.Errorf("older page: %d before=%v", rec.Code, f.lastQuery.Before)
	}
	if !strings.Contains(rec.Body.String(), `id="audit-log-older"`) || strings.Contains(rec.Body.String(), `>Older<`) {
		t.Error("last page keeps the wrapper but drops the Older button")
	}
}

// lastPageNext returns the Older href of the first page.
func (f *fakeAuditLog) lastPageNext(t *testing.T, g *rig) string {
	t.Helper()
	body := g.do("GET", "/admin/audit", "owner-token", nil, nil).Body.String()
	i := strings.Index(body, `href="/admin/audit?before=`)
	if i < 0 {
		t.Fatal("no Older link")
	}
	rest := body[i+len(`href="`):]
	return html.UnescapeString(rest[:strings.Index(rest, `"`)])
}

func TestAuditLogFiltersReachTheReader(t *testing.T) {
	t.Parallel()

	rows := auditRows(3)
	rows[1].Source, rows[1].Src, rows[1].Actor, rows[1].Action, rows[1].Result, rows[1].Error = "agent", auditSrcAgent, "ci-bot", "entities_update", "denied", "forbidden: scope"
	f := &fakeAuditLog{rows: rows, facets: AuditFacets{Actions: []string{"entity.publish", "entities_update"}}}
	g := newRig(t, func(d *Deps) { d.AuditLog = f })
	rec := g.do("GET", "/admin/audit?source=agent&action=entities_update&actor=n:ci-bot&from=2026-10-01&to=2026-10-03", "owner-token", nil, nil)
	body := rec.Body.String()
	q := f.lastQuery
	if rec.Code != 200 || q.Source != "agent" || q.Action != "entities_update" || q.Actor != "n:ci-bot" {
		t.Fatalf("query = %+v (%d)", q, rec.Code)
	}
	if !q.From.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) || !q.To.Equal(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("window = %v .. %v, want to inclusive", q.From, q.To)
	}
	for _, want := range []string{"ci-bot", ">Agent<", ">Refused<", "forbidden: scope", `id="row-m2"`, "From 1 Oct 2026"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "owner@example.com") {
		t.Error("filtered page shows a row the filters exclude")
	}
	body = g.do("GET", "/admin/audit?source=system", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "No matching entries") {
		t.Error("no filtered empty state")
	}
}

func TestAuditLogSearchAlwaysHasARange(t *testing.T) {
	t.Parallel()

	f := &fakeAuditLog{rows: auditRows(2)}
	g := newRig(t, func(d *Deps) { d.AuditLog = f })
	body := g.do("GET", "/admin/audit?q=page", "owner-token", nil, nil).Body.String()
	if f.lastQuery.Q != "page" || f.lastQuery.From.IsZero() {
		t.Fatalf("query = %+v", f.lastQuery)
	}
	if d := time.Until(f.lastQuery.From); d > -29*24*time.Hour || d < -32*24*time.Hour {
		t.Errorf("default from = %v ago, want about 30 days", -d)
	}
	if !strings.Contains(body, `value="`+f.lastQuery.From.Format("2006-01-02")+`"`) {
		t.Error("From input does not show the effective date")
	}
	g.do("GET", "/admin/audit?q=page&from=2020-01-01&to=2026-10-03", "owner-token", nil, nil)
	if days := f.lastQuery.To.Sub(f.lastQuery.From).Hours() / 24; days != 366 {
		t.Errorf("range = %v days, want 366", days)
	}
	g.do("GET", "/admin/audit?from=2020-01-01", "owner-token", nil, nil)
	if f.lastQuery.Q != "" || !f.lastQuery.From.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("filter without q must keep its own range: %+v", f.lastQuery)
	}
	g.do("GET", "/admin/audit?from=2026-10-05&to=2026-10-01", "owner-token", nil, nil)
	if !f.lastQuery.From.Before(f.lastQuery.To) {
		t.Errorf("reversed range not swapped: %+v", f.lastQuery)
	}
	body = g.do("GET", "/admin/audit?from=2026-10-05&to=2026-10-01", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, "From 1 Oct 2026") || !strings.Contains(body, "To 5 Oct 2026") || !strings.Contains(body, `value="2026-10-01"`) || !strings.Contains(body, `value="2026-10-05"`) {
		t.Error("inputs and chips do not show the swapped range")
	}
}

func TestAuditLogFacetWindowFollowsTheEndDate(t *testing.T) {
	t.Parallel()

	f := &fakeAuditLog{rows: auditRows(2)}
	g := newRig(t, func(d *Deps) { d.AuditLog = f })
	g.do("GET", "/admin/audit?to=2026-04-30", "owner-token", nil, nil)
	if !f.facetFrom.Before(f.facetTo) {
		t.Errorf("facet window inverted: %v to %v", f.facetFrom, f.facetTo)
	}
}

func TestAuditLogClampsExtremeDates(t *testing.T) {
	t.Parallel()

	f := &fakeAuditLog{rows: auditRows(2)}
	g := newRig(t, func(d *Deps) { d.AuditLog = f })
	rec := g.do("GET", "/admin/audit?from=0001-01-01&to=9999-12-31", "owner-token", nil, nil)
	q := f.lastQuery
	if rec.Code != 200 || q.From.Year() != 2000 || q.To.After(time.Now().AddDate(0, 0, 3)) {
		t.Errorf("extreme dates: %d from=%v to=%v", rec.Code, q.From, q.To)
	}
	if strings.Contains(rec.Body.String(), "9999") || strings.Contains(rec.Body.String(), "0001-01-01") {
		t.Error("page still shows the unclamped dates")
	}
}

func TestAuditLogClipsLongAgentErrors(t *testing.T) {
	t.Parallel()

	rows := auditRows(1)
	rows[0].Source, rows[0].Src, rows[0].Result, rows[0].Error = "agent", auditSrcAgent, "error", strings.Repeat("e", 500)
	g := newRig(t, func(d *Deps) { d.AuditLog = &fakeAuditLog{rows: rows} })
	body := g.do("GET", "/admin/audit", "owner-token", nil, nil).Body.String()
	if strings.Contains(body, strings.Repeat("e", 201)) || !strings.Contains(body, strings.Repeat("e", 200)+"…") {
		t.Error("agent error is not clipped to 200 runes")
	}
}

func TestAuditLogToleratesBadInput(t *testing.T) {
	t.Parallel()

	f := &fakeAuditLog{rows: auditRows(2), facetsErr: context.DeadlineExceeded}
	g := newRig(t, func(d *Deps) { d.AuditLog = f })
	rec := g.do("GET", "/admin/audit?from=nope&to=2026-13-40&before=garbage&actor=u:bad&source=zzz", "owner-token", nil, nil)
	q := f.lastQuery
	if rec.Code != 200 || !q.From.IsZero() || !q.To.IsZero() || q.Before != nil || q.Actor != "" || q.Source != "" {
		t.Errorf("bad input: %d %+v", rec.Code, q)
	}
	if !strings.Contains(rec.Body.String(), "Page 0") {
		t.Error("bad input should show the first page")
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
	if !strings.Contains(body, `href="/x-test/audit?before=`) || strings.Contains(body, `"/admin/`) {
		t.Error("Older link must use the configured admin path")
	}
	body = g.do("GET", "/x-test/audit?source=admin&from=2026-10-01", "owner-token", nil, nil).Body.String()
	if !strings.Contains(body, `href="/x-test/audit?source=admin"`) || strings.Contains(body, `"/admin/`) {
		t.Error("chips and clear link must use the configured admin path")
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
