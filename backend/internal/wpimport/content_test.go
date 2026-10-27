package wpimport

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

func TestConvertStripsBlockCommentsAndKeepsEmbeds(t *testing.T) {
	in := `<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph -->
<!-- wp:embed {"url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"} -->
<figure class="wp-block-embed"><div class="wp-block-embed__wrapper">
https://www.youtube.com/watch?v=dQw4w9WgXcQ
</div></figure>
<!-- /wp:embed -->
<p>https://vimeo.com/76979871</p>
<figure class="wp-block-embed"><div class="wp-block-embed__wrapper"><iframe src="https://www.youtube.com/embed/abcdefghijk?feature=oembed" width="500"></iframe></div></figure>
<iframe src="https://maps.example.com/embed"></iframe>`
	c := Convert(in, false)
	if strings.Contains(c.HTML, "<!--") {
		t.Errorf("comments left: %s", c.HTML)
	}
	for _, want := range []string{"youtube.com/embed/dQw4w9WgXcQ", "player.vimeo.com/video/76979871", "youtube.com/embed/abcdefghijk"} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("missing %s in %s", want, c.HTML)
		}
	}
	if strings.Contains(c.HTML, "maps.example.com") || len(c.Dropped) != 1 {
		t.Errorf("unsupported iframe kept: %s %v", c.HTML, c.Dropped)
	}
	if c.Embeds != 3 {
		t.Errorf("embeds = %d", c.Embeds)
	}
	// The sanitiser must keep the converted embeds.
	clean := entity.SanitizeRichText(c.HTML)
	if strings.Count(clean, "<iframe") != 3 {
		t.Errorf("sanitiser dropped an embed: %s", clean)
	}
}

func TestConvertFormsShortcodesBuilders(t *testing.T) {
	in := `<p>Intro</p><div class="wpcf7"><form>x</form></div><p>[contact-form-7 id="5"]</p><p>See [sic] and [1].</p>
<p>[acme_widget a="b"]</p><div data-elementor-type="wp-page"></div>`
	c := Convert(in, false)
	if strings.Contains(c.HTML, "wpcf7") || strings.Contains(c.HTML, "contact-form-7") || strings.Contains(c.HTML, "acme_widget") {
		t.Errorf("not removed: %s", c.HTML)
	}
	if !strings.Contains(c.HTML, "[sic]") || !strings.Contains(c.HTML, "[1]") {
		t.Errorf("non-shortcode text damaged: %s", c.HTML)
	}
	if got := strings.Join(c.Shortcodes, ","); got != "acme_widget,contact-form-7" {
		t.Errorf("shortcodes = %s", got)
	}
	if strings.Join(c.Forms, ",") != "Contact Form 7" || strings.Join(c.Builders, ",") != "Elementor" {
		t.Errorf("forms %v builders %v", c.Forms, c.Builders)
	}
}

func TestConvertRawClassicContent(t *testing.T) {
	in := "First para\nsecond line\n\n[caption id=\"a\"]<img src=\"/x.png\" alt=\"\"> A caption[/caption]\n\n<h2>Head</h2>"
	c := Convert(in, true)
	for _, want := range []string{"<p>First para<br/>", "<figure><img", "<figcaption>A caption</figcaption>", "<h2>Head</h2>"} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("missing %q in %s", want, c.HTML)
		}
	}
}

func TestRewrite(t *testing.T) {
	id := uuid.New()
	rw := &Rewriter{
		Hosts: map[string]bool{"old.example": true},
		Media: func(u *url.URL) (string, bool) {
			if mediaKey(u) == "2024/05/a.jpg" {
				return "/media/t/a.jpg", true
			}
			return "", false
		},
		Entity: func(p string, q url.Values) (EntityRef, bool) {
			if normPath(p) == "/2024/05/hello" {
				return EntityRef{ID: id, Path: "/2024/05/hello"}, true
			}
			return EntityRef{}, false
		},
	}
	in := `<p><a href="https://old.example/2024/05/hello/#top">post</a> <a href="https://old.example/about/">about</a>
<a href="https://elsewhere.test/x">ext</a> <img src="https://old.example/wp-content/uploads/2024/05/a-300x200.jpg" srcset="x 1w">
<img src="https://old.example/wp-content/uploads/gone.png"> <a href="https://old.example/wp-content/uploads/gone.pdf">pdf</a></p>`
	out, st := rw.Rewrite(in)
	for _, want := range []string{`data-entity-id="` + id.String() + `"`, `href="/2024/05/hello#top"`, `href="/about/"`, `src="/media/t/a.jpg"`, `href="https://elsewhere.test/x"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if strings.Contains(out, "old.example") || strings.Contains(out, "srcset") || strings.Contains(out, "wp-content") {
		t.Errorf("old URLs left: %s", out)
	}
	if st.MediaRewritten != 1 || len(st.Unresolved) != 2 || st.LinksRewritten != 2 {
		t.Errorf("stats %+v", st)
	}
	if left := WPContentURLs(out); len(left) != 0 {
		t.Errorf("WPContentURLs = %v", left)
	}
}

func TestMediaKeyStripsSizes(t *testing.T) {
	for in, want := range map[string]string{
		"https://x.test/wp-content/uploads/2024/05/a-300x200.jpg":  "2024/05/a.jpg",
		"https://x.test/wp-content/uploads/2024/05/a-scaled.jpg":   "2024/05/a.jpg",
		"https://x.test/wp-content/uploads/2024/05/a.jpg":          "2024/05/a.jpg",
		"https://x.test/wp-content/uploads/2024/05/my%20pic.png":   "2024/05/my pic.png",
		"https://x.test/wp-content/themes/t/img/logo-100x20.svg":   "wp-content/themes/t/img/logo.svg",
		"https://x.test/wp-content/uploads/2024/05/2020-12-31.jpg": "2024/05/2020-12-31.jpg",
	} {
		u, _ := url.Parse(in)
		if got := mediaKey(u); got != want {
			t.Errorf("mediaKey(%s) = %q, want %q", in, got, want)
		}
	}
}
