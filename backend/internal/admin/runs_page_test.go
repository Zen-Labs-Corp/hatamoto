package admin

import (
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

func TestRunsTab(t *testing.T) {
	t.Parallel()

	rv := runView("finished", "transform")
	rv.ChangesetID = 42
	g, _, _, _ := newRunsRig(t, rv)
	rec := g.do("GET", "/admin/plugins/hello/runs", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("runs tab: %d", rec.Code)
	}
	// The host ticks and the Start form are core's: after the frame, never inside it.
	frame := strings.Index(body, "data-hm-pluginframe ")
	frameEnd := strings.Index(body, "</noscript>")
	ticks := strings.Index(body, `name="hosts"`)
	if frame < 0 || frameEnd < frame || ticks < frameEnd {
		t.Errorf("host ticks must follow the sealed frame (frame %d-%d, ticks %d)", frame, frameEnd, ticks)
	}
	for _, want := range []string{
		`action="/admin/plugins/hello/runs/import/start"`, `data-hm-run-form`, `data-hm-run-input-names="site user"`,
		`name="input_site"`, `name="input_user"`, "Shown by core", "Only for this import", "Start import",
		`value="old.example.com"`, `value="cdn.example.com"`, `href="/admin/plugins/hello/changesets/42"`,
		`aria-current="page">Runs`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("runs tab lacks %q", want)
		}
	}
	// The plugin's own host is fixed (on, disabled, still posted); the other is unusual and unticked.
	fixed := regexp.MustCompile(`<input type="hidden" name="hosts" value="old.example.com"><div class="hm-field">\s*<label[^>]*><input[^>]*value="old.example.com"[^>]* checked[^>]* disabled`)
	if !fixed.MatchString(body) {
		t.Error("the manifest host is checked, disabled and posted by a hidden field")
	}
	if !strings.Contains(body, "unusual") || strings.Count(body, "unusual") != 1 {
		t.Error("only the host outside the manifest is unusual")
	}
	if regexp.MustCompile(`value="cdn.example.com"[^>]* checked`).MatchString(body) {
		t.Error("an unusual host starts unticked")
	}
	if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) > 0 {
		t.Errorf("a11y: %s", strings.Join(issues, "; "))
	}
}

func TestRunsTabGuards(t *testing.T) {
	t.Parallel()

	g, _, _, _ := newRunsRig(t)
	if rec := g.do("GET", "/admin/plugins/hello/runs", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d, want 403", rec.Code)
	}
	if rec := g.do("GET", "/admin/plugins/ghost/runs", "owner-token", nil, nil); rec.Code != 404 {
		t.Errorf("unknown plugin: %d, want 404", rec.Code)
	}
	g2 := newRig(t, func(*Deps) {})
	if rec := g2.do("GET", "/admin/plugins/hello/runs", "owner-token", nil, nil); rec.Code != 503 {
		t.Errorf("not wired: %d, want 503", rec.Code)
	}
}

func TestRunsTabMessage(t *testing.T) {
	t.Parallel()

	const msg = "Imported 87 events, 13 skipped (duplicates)"
	cancelled := runView("failed", "fetch")
	cancelled.Reason = "cancelled"
	tests := []struct {
		name  string
		rv    RunView
		extra string
	}{
		{"finished", runView("finished", "transform"), ""},
		{"failed", runView("failed", "fetch"), ""},
		{"cancelled", cancelled, "Cancelled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rv := tc.rv
			rv.Message = msg
			g, _, _, _ := newRunsRig(t, rv)
			rec := g.do("GET", "/admin/plugins/hello/runs", "owner-token", nil, nil)
			if rec.Code != 200 {
				t.Fatalf("runs tab: %d", rec.Code)
			}
			body := rec.Body.String()
			for _, want := range []string{`title="` + msg + `"`, "hm-table__clip", ">Message<", `href="/admin/plugins/hello/runs/` + rv.ID.String() + `"`, tc.extra} {
				if !strings.Contains(body, want) {
					t.Errorf("runs tab lacks %q", want)
				}
			}
			if issues := a11y.Check(body, a11y.Options{Page: true}); len(issues) != 0 {
				t.Errorf("a11y: %v", issues)
			}
		})
	}

	t.Run("escaped", func(t *testing.T) {
		t.Parallel()
		rv := runView("finished", "transform")
		rv.Message = "<b>bold</b> done"
		g, _, _, _ := newRunsRig(t, rv)
		body := g.do("GET", "/admin/plugins/hello/runs", "owner-token", nil, nil).Body.String()
		if !strings.Contains(body, "&lt;b&gt;bold&lt;/b&gt; done") || strings.Contains(body, "<b>bold</b>") {
			t.Error("a message is escaped, never rendered as markup")
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		g, _, _, _ := newRunsRig(t, runView("finished", "transform"))
		body := g.do("GET", "/admin/plugins/hello/runs", "owner-token", nil, nil).Body.String()
		if !strings.Contains(body, "—") {
			t.Error("a run without a message shows a dash")
		}
	})
}
