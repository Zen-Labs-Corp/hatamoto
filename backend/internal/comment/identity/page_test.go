package identity

import (
	"strings"
	"testing"
)

func render(t *testing.T, d PageData) string {
	t.Helper()
	var b strings.Builder
	if err := RenderPage(&b, d); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRenderPage_ConfirmHasPostButtonOnly(t *testing.T) {
	t.Parallel()
	out := render(t, PageData{SiteName: "Site S", EntityTitle: "A title", Action: "/comments/confirm/tok", State: PageConfirm})
	for _, want := range []string{`method="post"`, `action="/comments/confirm/tok"`, "Site S", "A title", `name="robots" content="noindex"`, `no-referrer`, `lang="en"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	for _, bad := range []string{"<script", "<input", "http://", "https://", "\u2013", "\u2014"} {
		if strings.Contains(out, bad) {
			t.Errorf("unexpected %q", bad)
		}
	}
}

func TestRenderPage_EscapesTitle(t *testing.T) {
	t.Parallel()
	out := render(t, PageData{SiteName: "S", EntityTitle: "<img src=x onerror=1>", Action: "/comments/confirm/t"})
	if strings.Contains(out, "<img") || !strings.Contains(out, "&lt;img") {
		t.Errorf("title not escaped: %s", out)
	}
}

func TestRenderPage_InvalidRevealsNothing(t *testing.T) {
	t.Parallel()
	out := render(t, PageData{SiteName: "Site S", EntityTitle: "A title", Action: "/comments/confirm/tok", State: PageInvalid})
	for _, bad := range []string{"A title", "/comments/confirm/tok", "<form", "<script", "\u2013", "\u2014"} {
		if strings.Contains(out, bad) {
			t.Errorf("invalid page contains %q", bad)
		}
	}
	if !strings.Contains(out, "expired or was already used") {
		t.Errorf("missing message: %s", out)
	}
}
