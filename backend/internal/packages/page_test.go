package packages

import (
	"crypto/sha256"
	"encoding/base64"
	"html"
	"regexp"
	"strings"
	"testing"
)

func pageIndex() *Index {
	return &Index{Version: 1, Packages: []Entry{
		{Kind: KindPlugin, Name: "alpha", Version: "v1.2.0", Description: "Does alpha things", Permissions: []string{"db", "http"}, Author: "acme", Tier: TierCommunity, Source: "https://example.org/acme/alpha"},
		{Kind: KindPlugin, Name: "beta", Version: "0.1.0", Tier: TierFirstParty},
		{Kind: KindTheme, Name: "sunrise", Version: "v1.0.0", Description: "a theme"},
	}}
}

func renderPage(t *testing.T, idx *Index, opts PageOptions) string {
	t.Helper()
	out, err := RenderPage(idx, opts)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestRenderPageCardsForPluginsOnly(t *testing.T) {
	out := renderPage(t, pageIndex(), PageOptions{})
	if n := strings.Count(out, "<article"); n != 2 {
		t.Fatalf("%d cards, want 2", n)
	}
	for _, want := range []string{"alpha v1.2.0", "beta v0.1.0", "Community", "First-party", "by @acme", "Does alpha things", "2 permissions", "<details", "Keep its own data tables", "No permissions", `href="https://example.org/acme/alpha"`, `id="q"`, "addEventListener"} {
		if !strings.Contains(out, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(out, "sunrise") {
		t.Error("a theme was listed")
	}
	if strings.Index(out, "alpha v1") > strings.Index(out, "beta v0") {
		t.Error("cards are not in index order")
	}
}

func TestRenderPageButtonsOnlyWhenSet(t *testing.T) {
	out := renderPage(t, pageIndex(), PageOptions{})
	if strings.Contains(out, "Write a plugin") || strings.Contains(out, "Submit a plugin") || strings.Contains(out, `class="hint"`) {
		t.Error("buttons or hint shown without options")
	}
	out = renderPage(t, pageIndex(), PageOptions{WriteURL: "https://example.org/w", SubmitURL: "https://example.org/s", InstallHint: "Install it from the admin"})
	for _, want := range []string{`href="https://example.org/w"`, `href="https://example.org/s"`, "Install it from the admin"} {
		if !strings.Contains(out, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	out = renderPage(t, pageIndex(), PageOptions{WriteURL: "javascript:alert(1)", SubmitURL: "http://example.org/s"})
	if strings.Contains(out, "Write a plugin") || strings.Contains(out, "Submit a plugin") {
		t.Error("an unsafe button URL produced a button")
	}
}

func TestRenderPageTitleAndAuthor(t *testing.T) {
	idx := &Index{Packages: []Entry{
		{Kind: KindPlugin, Name: "gtm", Title: "Google Tag Manager", Version: "0.1.0", Author: "Hatamoto", Tier: TierFirstParty},
		{Kind: KindPlugin, Name: "alpha", Version: "1.0.0", Author: "acme", Tier: TierCommunity},
	}}
	out := renderPage(t, idx, PageOptions{})
	if !strings.Contains(out, "<h2>Google Tag Manager</h2>") || !strings.Contains(out, "gtm v0.1.0") {
		t.Error("title heading or the name/version line missing")
	}
	if !strings.Contains(out, "<h2>alpha</h2>") {
		t.Error("a card without a title must head with its name")
	}
	if strings.Contains(out, "@Hatamoto") || !strings.Contains(out, "by @acme") {
		t.Error("by @author must show for community cards only")
	}
}

func TestRenderPageEscapesHostileText(t *testing.T) {
	idx := &Index{Packages: []Entry{{Kind: KindPlugin, Name: "evil", Version: "v1.0.0",
		Description: "<script>alert(1)</script>", Author: `"><img src=x onerror=alert(2)>`, Tier: TierCommunity}}}
	out := renderPage(t, idx, PageOptions{Title: "<b>t</b>"})
	for _, bad := range []string{"<script>alert(1)", "<img src=x", "<b>t</b>"} {
		if strings.Contains(out, bad) {
			t.Errorf("unescaped %q in page", bad)
		}
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("description not shown escaped")
	}
}

func TestRenderPageRefusesUnsafeSourceLinks(t *testing.T) {
	for _, src := range []string{"javascript:alert(1)", "http://example.org/x", "data:text/html,hi", "https://u:p@example.org/x", ""} {
		idx := &Index{Packages: []Entry{{Kind: KindPlugin, Name: "p", Version: "v1.0.0", Source: src}}}
		out := renderPage(t, idx, PageOptions{})
		if strings.Contains(out, "Source") || strings.Contains(out, "javascript:") || strings.Contains(out, "data:text") {
			t.Errorf("source %q produced a link", src)
		}
	}
}

func TestRenderPageIsSelfContained(t *testing.T) {
	out := renderPage(t, pageIndex(), PageOptions{WriteURL: "https://example.org/w"})
	for _, bad := range []string{"url(", "@import", "<link"} {
		if strings.Contains(out, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
	if strings.Contains(out, "src=") {
		t.Error("page loads a remote asset")
	}
	if !strings.Contains(out, "Content-Security-Policy") {
		t.Error("page lacks its CSP")
	}
	allowed := map[string]bool{"https://example.org/acme/alpha": true, "https://example.org/w": true}
	for _, m := range regexp.MustCompile(`href="([^"]*)"`).FindAllStringSubmatch(out, -1) {
		if !allowed[m[1]] {
			t.Errorf("unexpected link %q", m[1])
		}
	}
}

// inlineBlocks returns the exact bytes between <tag> and </tag> for every such
// element of the rendered page.
func inlineBlocks(out, tag string) []string {
	var blocks []string
	for {
		_, rest, ok := strings.Cut(out, "<"+tag+">")
		if !ok {
			return blocks
		}
		body, after, ok := strings.Cut(rest, "</"+tag+">")
		if !ok {
			return blocks
		}
		blocks = append(blocks, body)
		out = after
	}
}

func cspHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

func pageMetaCSP(t *testing.T, out string) string {
	t.Helper()
	m := regexp.MustCompile(`<meta http-equiv="Content-Security-Policy" content="([^"]*)">`).FindStringSubmatch(out)
	if m == nil {
		t.Fatal("page has no CSP meta tag")
	}
	return html.UnescapeString(m[1])
}

// The CSP allows the page's inline style and script by hash and nothing else inline.
func TestRenderPageCSPHashesMatchTheInlineBlocks(t *testing.T) {
	// A hostile card must not be able to add a third inline block (it is escaped).
	idx := pageIndex()
	idx.Packages = append(idx.Packages, Entry{Kind: KindPlugin, Name: "evil", Version: "1", Description: "</style><script>alert(1)</script>", Tier: TierCommunity, Author: "x"})
	out := renderPage(t, idx, PageOptions{})
	csp := pageMetaCSP(t, out)
	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-hashes") || strings.Contains(csp, "nonce-") {
		t.Fatalf("CSP is not hash-only: %s", csp)
	}
	for _, d := range []struct{ tag, directive string }{{"style", "style-src"}, {"script", "script-src"}} {
		blocks := inlineBlocks(out, d.tag)
		if len(blocks) != 1 {
			t.Fatalf("%d inline <%s> elements, want exactly 1", len(blocks), d.tag)
		}
		want := d.directive + " " + cspHash(blocks[0])
		if !strings.Contains(csp, want+";") {
			t.Errorf("CSP lacks %s for the rendered <%s> block:\n%s", want, d.tag, csp)
		}
	}
	for _, bad := range []string{"style=", " onclick=", " onload=", " oninput="} {
		if strings.Contains(out, bad) {
			t.Errorf("page has an inline %q that the CSP would block", bad)
		}
	}
}

// A one-character edit of a block changes its hash, so the check above is not vacuous.
func TestRenderPageCSPHashDetectsAnEditedBlock(t *testing.T) {
	out := renderPage(t, pageIndex(), PageOptions{})
	csp := pageMetaCSP(t, out)
	for _, tag := range []string{"style", "script"} {
		edited := strings.Replace(out, "<"+tag+">", "<"+tag+">/* x */", 1)
		blocks := inlineBlocks(edited, tag)
		if len(blocks) != 1 || strings.Contains(csp, cspHash(blocks[0])) {
			t.Errorf("an edited <%s> block still matches the CSP", tag)
		}
	}
}

func TestRenderPagePermissionWarning(t *testing.T) {
	idx := &Index{Packages: []Entry{{Kind: KindPlugin, Name: "tags", Version: "1.0.0", Permissions: []string{"http.fetch.any", "site.scripts"}}}}
	out := renderPage(t, idx, PageOptions{})
	for _, want := range []string{"2 permissions", `aria-hidden="true">⚠</span><span class="vh">Warning: Any web address. JavaScript on every public page.</span>`, "⚠ <span class=\"mono\">http.fetch.any</span>", "send requests to ANY public web address"} {
		if !strings.Contains(out, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(out, "WARNING:") || strings.Contains(out, "Needs:") || strings.Contains(out, "<details open") {
		t.Error("page has a WARNING: prefix, a Needs: line or an open details")
	}
}

func TestRenderPageNoPermissionsCard(t *testing.T) {
	idx := &Index{Packages: []Entry{{Kind: KindPlugin, Name: "hello", Version: "1.0.0"}}}
	out := renderPage(t, idx, PageOptions{})
	if !strings.Contains(out, "No permissions") || strings.Contains(out, "<details") {
		t.Error("an empty card must say No permissions and have no details box")
	}
}
