package email

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunDigestTemplate(t *testing.T) {
	svc := newTestService(t)
	var b bytes.Buffer
	data := RunDigestData{SiteName: "Acme <Co>", HistoryURL: "https://example.test/a/settings/changes", Runs: []RunDigestRow{
		{Plugin: "pricer", Run: "prices", Changes: 12, URL: "https://example.test/a/plugins/pricer/changesets/4"},
		{Plugin: "stock", Run: "levels", Changes: 1, URL: "https://example.test/a/plugins/stock/changesets/5"},
	}}
	if err := svc.templates.ExecuteTemplate(&b, "run_digest.html", data); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"Acme &lt;Co&gt;", `href="https://example.test/a/plugins/pricer/changesets/4"`, "pricer, prices</a>: 12 changes", "stock, levels</a>: 1 change<", `href="https://example.test/a/settings/changes"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.ContainsAny(out, "\u2013\u2014") {
		t.Error("the mail must not contain dashes used as punctuation")
	}
}

func TestRunDigestSendIsANoOpWhenUnconfigured(t *testing.T) {
	svc := newTestService(t)
	if err := svc.SendRunDigest(context.Background(), "a@example.test", RunDigestData{SiteName: "S", HistoryURL: "/x"}); err != nil {
		t.Error(err)
	}
}

func TestRunDigestListsAutomaticCreatesSeparately(t *testing.T) {
	svc := newTestService(t)
	render := func(runs ...RunDigestRow) string {
		var b bytes.Buffer
		data := RunDigestData{SiteName: "Acme", HistoryURL: "https://example.test/a/settings/changes", Runs: runs}
		if err := svc.templates.ExecuteTemplate(&b, "run_digest.html", data); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	update := RunDigestRow{Plugin: "pricer", Run: "prices", Changes: 2, URL: "https://example.test/a/plugins/pricer/changesets/4"}
	if out := render(update); strings.Contains(out, "New entries") || !strings.Contains(out, "</ul>\n\n<p") {
		t.Errorf("an update-only digest must not change:\n%s", out)
	}
	out := render(update,
		RunDigestRow{Plugin: "news", Run: "feed", Created: 3, URL: "https://example.test/a/plugins/news/changesets/6"},
		RunDigestRow{Plugin: "blog", Run: "posts", Created: 1, Changes: 1, URL: "https://example.test/a/plugins/blog/changesets/7"})
	for _, want := range []string{"New entries published automatically", "news, feed</a>: 3 new entries", "blog, posts</a>: 1 new entry<", "blog, posts</a>: 1 change<", "pricer, prices</a>: 2 changes"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "news, feed</a>: 0 changes") {
		t.Error("a run that only created entries must not appear in the updates list")
	}
	if strings.ContainsAny(out, "\u2013\u2014") {
		t.Error("the mail must not contain dashes used as punctuation")
	}
}
