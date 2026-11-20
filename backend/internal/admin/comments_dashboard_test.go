package admin

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/admin/a11y"
)

// cdashRig is the Overview rig with a comment service wired.
func cdashRig(t *testing.T, svc *fakeComments) *dashRig {
	t.Helper()
	g := newDashRig(t)
	g.a.deps.Comments = svc
	g.a.deps.BaseURL = func(*http.Request) string { return "https://example.com" }
	return g
}

func cdashPage(t *testing.T, g *dashRig, token string) string {
	t.Helper()
	rec := g.do("GET", "/admin", token, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %v", issues)
	}
	return body
}

func TestDashCommentsCard(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{settings: CommentSettings{Enabled: true}, counts: CommentCounts{Pending: 12},
		rows: []CommentRow{{ID: "c1", AuthorName: "Ada", EntityTitle: "Alpha service", BodyRaw: "First\nline " + strings.Repeat("x", 100), CreatedAt: time.Now()}}}
	g := cdashRig(t, svc)
	for _, token := range []string{"owner-token", "editor-token"} {
		body := cdashPage(t, g, token)
		for _, want := range []string{`id="dash-comments"`, "12 pending", `href="/admin/comments?status=pending"`, "Moderate", "Ada on Alpha service", "First line"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s page lacks %q", token, want)
			}
		}
		if strings.Contains(body, strings.Repeat("x", 100)) {
			t.Errorf("%s: the excerpt is not cut", token)
		}
	}
	if svc.lastFilter.Status != "pending" || svc.lastFilter.PerPage != cdashRows {
		t.Errorf("queue filter %+v", svc.lastFilter)
	}
}

func TestDashCommentsOnWithNothingWaiting(t *testing.T) {
	t.Parallel()

	body := cdashPage(t, cdashRig(t, &fakeComments{settings: CommentSettings{Enabled: true}}), "owner-token")
	for _, want := range []string{`id="dash-comments"`, "No comments waiting", "0 pending"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestDashCommentsOffNoCard(t *testing.T) {
	t.Parallel()

	svc := &fakeComments{}
	g := cdashRig(t, svc)
	if body := cdashPage(t, g, "owner-token"); strings.Contains(body, "dash-comments") {
		t.Error("the card shows with the switch off and nothing pending")
	}
	// Off but comments still waiting: the card is a nudge to moderate them.
	svc.mu.Lock()
	svc.counts.Pending = 3
	svc.mu.Unlock()
	if body := cdashPage(t, g, "owner-token"); !strings.Contains(body, `id="dash-comments"`) || !strings.Contains(body, "3 pending") {
		t.Error("pending comments are hidden while the switch is off")
	}
	// No service: no card and no call.
	if body := cdashPage(t, newDashRig(t), "owner-token"); strings.Contains(body, "dash-comments") {
		t.Error("card without a service")
	}
}

func TestDashCommentsError(t *testing.T) {
	t.Parallel()

	g := cdashRig(t, &fakeComments{err: errors.New("db down")})
	body := cdashPage(t, g, "owner-token")
	for _, want := range []string{`id="dash-comments"`, "Comments could not be loaded.", "Recent submissions", "Published"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

const cdashWarn = "Confirmation emails are not being sent: set your site address in Settings &gt; General."

func TestDashCommentsWarnsWithoutAddress(t *testing.T) {
	t.Parallel()

	rows := []CommentRow{{ID: "c1", AuthorName: "Ada", EntityTitle: "Alpha service", BodyRaw: "Hi", CreatedAt: time.Now()}}
	for name, svc := range map[string]*fakeComments{
		"rows":  {settings: CommentSettings{Enabled: true}, counts: CommentCounts{Pending: 12}, rows: rows},
		"empty": {settings: CommentSettings{Enabled: true}},
	} {
		g := cdashRig(t, svc)
		g.a.deps.BaseURL = func(*http.Request) string { return "" }
		for _, token := range []string{"owner-token", "editor-token"} {
			body := cdashPage(t, g, token)
			_, card, _ := strings.Cut(body, `id="dash-comments"`)
			if !strings.Contains(card, cdashWarn) {
				t.Errorf("%s/%s: no warning in the card", name, token)
			}
			if name == "empty" && !strings.Contains(card, "No comments waiting") {
				t.Errorf("%s: no empty state", token)
			}
		}
	}
}

func TestDashCommentsNoWarnWithAddress(t *testing.T) {
	t.Parallel()

	body := cdashPage(t, cdashRig(t, &fakeComments{settings: CommentSettings{Enabled: true}}), "owner-token")
	if strings.Contains(body, "Confirmation emails are not being sent") {
		t.Error("warning shown with an address set")
	}
}

func TestDashCommentsNoWarnWhenOff(t *testing.T) {
	t.Parallel()

	g := cdashRig(t, &fakeComments{counts: CommentCounts{Pending: 3}})
	g.a.deps.BaseURL = func(*http.Request) string { return "" }
	body := cdashPage(t, g, "owner-token")
	if !strings.Contains(body, `id="dash-comments"`) || strings.Contains(body, "Confirmation emails are not being sent") {
		t.Error("want the card without the warning")
	}
}
