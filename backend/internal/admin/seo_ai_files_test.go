package admin

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

func fakeAIFilesStatus(context.Context) (AIFilesStatus, error) {
	at := time.Date(2026, 11, 22, 14, 2, 0, 0, time.UTC)
	return AIFilesStatus{
		LLMs: AIFileStatus{ChangedAt: &at, Bytes: 3100},
		Full: AIFileStatus{ChangedAt: &at, Bytes: 412 << 10},
		Changes: []AIFileChange{
			{At: at, Text: "Elena Voss: job title changed"},
			{At: at.Add(-29 * time.Hour), Text: "Pricing: added"},
		},
	}, nil
}

func TestSEOAIFilesSave(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.Home = "%sitename%"
	form := url.Values{"ai_llms": {"true"}, "ai_full": {"false"}, "ai_summary": {"  Acme Studio builds sites.  "}, "ai_posts": {"5"}}
	rec := s.do("POST", "/admin/settings/seo/ai-files", "owner-token", form, nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/settings/seo/ai-files" {
		t.Fatalf("save = %d %s: %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	got := s.svc.set.AIFiles
	if got.LLMsOff || !got.FullOff || got.Summary != "Acme Studio builds sites." || got.Posts != 5 {
		t.Fatalf("ai files = %+v", got)
	}
	if s.svc.set.Home != "%sitename%" {
		t.Errorf("another tab's keys were clobbered: %+v", s.svc.set)
	}
	if s.svc.merges != 1 || s.flushed != 1 {
		t.Errorf("merges = %d, flushed = %d, want 1 each", s.svc.merges, s.flushed)
	}
	if len(s.events) != 1 || s.events[0].Action != "seo.settings" {
		t.Fatalf("audit = %+v", s.events)
	}
}

func TestSEOAIFilesKeepsUnpostedAndDefaults(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.AIFiles.FullOff = true
	s.svc.set.AIFiles.FullCapKB = 128
	s.svc.set.AIFiles.Posts = 7
	if rec := s.do("POST", "/admin/settings/seo/ai-files", "owner-token", url.Values{"ai_posts": {"20"}}, nil); rec.Code != 303 {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body)
	}
	got := s.svc.set.AIFiles
	if !got.FullOff || got.FullCapKB != 128 || got.Posts != 0 {
		t.Errorf("ai files = %+v, want the default posts stored as 0 and the rest kept", got)
	}
}

func TestSEOAIFilesRejectsBadInput(t *testing.T) {
	for name, tc := range map[string]struct {
		form url.Values
		want string
	}{
		"posts zero":     {url.Values{"ai_posts": {"0"}}, "ai-posts"},
		"posts too many": {url.Values{"ai_posts": {"201"}}, "ai-posts"},
		"posts text":     {url.Values{"ai_posts": {"x"}}, "ai-posts"},
		"summary long":   {url.Values{"ai_summary": {strings.Repeat("a", 301)}}, "ai-summary"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSEORig(t, newFakeSEO())
			rec := s.do("POST", "/admin/settings/seo/ai-files", "owner-token", tc.form, nil)
			if rec.Code != 422 || !strings.Contains(rec.Body.String(), `id="`+tc.want+`"`) {
				t.Fatalf("save = %d, want 422 naming %s", rec.Code, tc.want)
			}
			if s.svc.merges != 0 || s.flushed != 0 {
				t.Errorf("a rejected save changed something")
			}
		})
	}
}

func TestSEOAIFilesEditorForbidden(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	for _, m := range []string{"GET", "POST"} {
		if rec := s.do(m, "/admin/settings/seo/ai-files", "editor-token", url.Values{"ai_posts": {"5"}}, nil); rec.Code != 403 {
			t.Errorf("editor %s = %d, want 403", m, rec.Code)
		}
	}
	if s.svc.merges != 0 {
		t.Errorf("editor changed settings")
	}
}

func TestSEOAIFilesPageLists(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	body := s.get(t, "/admin/settings/seo/ai-files")
	for _, want := range []string{
		`name="ai_llms"`, `name="ai_full"`, `name="ai_summary"`, `name="ai_posts"`, `value="20"`,
		`href="/llms.txt"`, "Not built yet", "No changes recorded yet",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestSEOAIFilesPageGolden(t *testing.T) {
	s := newSEORig(t, newFakeSEO())
	s.svc.set.AIFiles.Summary = "Acme Studio builds websites for small firms in Lisbon."
	s.a.deps.AIFiles = fakeAIFilesStatus
	compareGolden(t, "pages/settings-seo-ai-files.html", normalizePage(s.get(t, "/admin/settings/seo/ai-files")))
}
