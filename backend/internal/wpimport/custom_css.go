package wpimport

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/render"
)

// cssSize reads "1.2 KB".
func cssSize(css string) string { return fmt.Sprintf("%.1f KB", float64(len(css))/1024) }

// planCustomCSS decides what the import stores for css and what the report
// says. CSS that passes the owner-CSS validator is stored as it is. Refused
// CSS is kept as a comment, so nothing is lost and none of it applies; when
// even that is over the size limit only the report keeps the reason.
func planCustomCSS(css string) (store, status string) {
	css = strings.ReplaceAll(css, "\r\n", "\n")
	size := cssSize(css)
	issue := render.ValidateCustomCSS(css)
	if issue == nil {
		return css, "imported (" + size + ") into Theme > Additional CSS"
	}
	body := strings.ReplaceAll(strings.ReplaceAll(css, "\x00", ""), "*/", "* /")
	commented := "/* Skipped by the WordPress import (" + issue.Error() + "): " + body + " */\n"
	if render.ValidateCustomCSS(commented) != nil {
		return "", "refused: " + issue.Error() + "; too large to keep as a comment"
	}
	return commented, "refused: " + issue.Error() + "; kept as a comment in Theme > Additional CSS"
}

// customCSSValue is the JSON string stored under render.AdditionalCSSKey. It
// escapes <, > and & (like the v1 encoder) so a stored "</style>" can never
// close a style element if the value reaches HTML.
func customCSSValue(store string) []byte {
	raw, _ := jsonx.Marshal(store, jsonx.HTMLSafe)
	return raw
}

func (r *Runner) setCustomCSSStatus(s string) {
	if r.res.Report != nil {
		r.res.Report.CustomCSS = s
	}
}

// importCustomCSS stores the old site's custom CSS in Theme > Additional CSS.
// A failure is a warning: the content import goes on.
func (r *Runner) importCustomCSS(ctx context.Context) {
	css := r.dump.CustomCSS
	if strings.TrimSpace(css) == "" {
		return
	}
	store, status := planCustomCSS(css)
	if store != "" {
		if err := r.d.Site.PatchThemeExtra(ctx, render.AdditionalCSSKey, customCSSValue(store)); err != nil {
			r.res.Warnings = append(r.res.Warnings, "custom CSS not imported: "+err.Error())
			status = "not imported: " + err.Error()
		}
	}
	r.setCustomCSSStatus(status)
	r.say("settings", "custom CSS: %s", status)
}

// customCSS reads the newest published item of the custom_css type, "" when
// there is none or it cannot be read.
func (s *RESTSource) customCSS(ctx context.Context, base string) string {
	var posts []restPost
	q := url.Values{"status": {"publish"}, "per_page": {"1"}, "orderby": {"modified"}, "context": {"edit"}}
	if _, err := s.c.JSON(ctx, "/wp/v2/"+base, q, &posts); err != nil || len(posts) == 0 {
		return ""
	}
	if c := posts[0].Content; c.Raw != "" {
		return c.Raw
	}
	return posts[0].Content.Rendered
}
