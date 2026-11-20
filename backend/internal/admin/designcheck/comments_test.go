package designcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Comments admin: the enable flow keeps its two warnings word for word, the tick-to-confirm
// buttons are never disabled by server HTML (script enables them; the server re-checks), and
// the comment screens never ask for a website field.
func TestCommentsEnableWarningsAndAck(t *testing.T) {
	t.Parallel()

	settings := readWeb(t, "pages/settings/comments.html")
	enable := readWeb(t, "pages/settings/comments-enable.html")
	if want := "Turning comments on will invite spam. Bots and spammers find comment forms within days. Every comment with a link and every first comment will wait in your moderation queue."; !strings.Contains(settings, want) {
		t.Errorf("comments.html lacks the warning %q", want)
	}
	for name, src := range map[string]string{"comments.html": settings, "comments-enable.html": enable} {
		for _, want := range []string{
			"Are you sure? Turning comments on will invite spam to this site. You will need to check the moderation queue regularly.",
			"I understand this will invite spam",
		} {
			if !strings.Contains(src, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
		if !strings.Contains(src, "data-hm-ack-submit") {
			t.Errorf("%s has no data-hm-ack-submit button", name)
		}
		disabled := regexp.MustCompile(`(?i)disabled`)
		for _, line := range strings.Split(src, "\n") {
			if !strings.Contains(line, "data-hm-ack-submit") {
				continue
			}
			if disabled.MatchString(line) {
				t.Errorf("%s: a data-hm-ack-submit button is disabled in the HTML: %s", name, line)
			}
		}
	}
}

func TestCommentsScreensHaveNoWebsiteField(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join(webAdmin, "pages", "comments", "*.html"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no comment templates found: %v", err)
	}
	files = append(files, filepath.Join(webAdmin, "partials", "comments-card.html"))
	bad := regexp.MustCompile(`name="(author_)?url"|name="website"`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := bad.FindString(string(b)); m != "" {
			t.Errorf("%s has the field %s: comments have no website field", filepath.Base(f), m)
		}
	}
}
