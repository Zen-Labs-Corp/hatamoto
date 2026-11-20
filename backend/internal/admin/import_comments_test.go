package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
	"hatamoto.dev/core/internal/wpimport"
)

func TestImportReportShowsTheCommentsCard(t *testing.T) {
	g, f, ids := importRig(t)
	run := f.runs[ids["done"]]
	res := *run.Result
	res.Comments = &wpimport.CommentsResult{
		Imported: 412, Approved: 380, Pending: 20, Spam: 9, Trash: 3, CommentersCreated: 133,
		Pings: []string{"First post (#8)", "Second post (#9)"}, PingsTotal: 17, SkippedNoEmail: 4, URLsDropped: 250,
	}
	run.Result = &res
	body := g.do("GET", "/admin/settings/import/wordpress/"+ids["done"].String(), "owner-token", nil, nil).Body.String()
	for _, want := range []string{
		`id="comments"`, ">Comments<", "<dt>Comments imported</dt><dd>412</dd>", "<dt>Approved</dt><dd>380</dd>", "<dt>Commenters created</dt><dd>133</dd>",
		"<dt>Skipped without an email address</dt><dd>4</dd>", "<dt>Author website addresses dropped</dt><dd>250</dd>",
		"First post (#8)", "And 15 more.", "Comments remain off. Turn them on in Settings &gt; Comments.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	card := body[strings.Index(body, `id="comments"`):]
	card = card[:strings.Index(card, "</section>")]
	for _, bad := range []string{"@", "url-sentinel", "203.0.113", "http"} {
		if strings.Contains(card, bad) {
			t.Errorf("comments card contains %q", bad)
		}
	}
	if issues := a11y.Check(normalizePage(body), a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}

	// With comments on, the "remain off" sentence goes away.
	res.Comments = &wpimport.CommentsResult{Imported: 1, Approved: 1, Enabled: true}
	body = g.do("GET", "/admin/settings/import/wordpress/"+ids["done"].String(), "owner-token", nil, nil).Body.String()
	if strings.Contains(body, "Comments remain off") || !strings.Contains(body, `id="comments"`) {
		t.Errorf("enabled comments page wrong")
	}
}

func TestImportReportWithoutCommentsHasNoCard(t *testing.T) {
	g, _, ids := importRig(t)
	body := g.do("GET", "/admin/settings/import/wordpress/"+ids["done"].String(), "owner-token", nil, nil).Body.String()
	if strings.Contains(body, `id="comments"`) || strings.Contains(body, "Comments remain off") {
		t.Error("a run without comment results shows a Comments card")
	}
}

func TestImportScanShowsCommentCounts(t *testing.T) {
	g, f, ids := importRig(t)
	rep := *f.runs[ids["scanned"]].Report
	rep.Comments, rep.CommentPings = 5, 2
	f.runs[ids["scanned"]].Report = &rep
	body := g.do("GET", "/admin/settings/import/wordpress/"+ids["scanned"].String(), "owner-token", nil, nil).Body.String()
	for _, want := range []string{"<dt>Comments</dt><dd>5</dd>", "Pingbacks and trackbacks (skipped)</dt><dd>2</dd>"} {
		if !strings.Contains(body, want) {
			t.Errorf("scan lacks %q", want)
		}
	}
}
