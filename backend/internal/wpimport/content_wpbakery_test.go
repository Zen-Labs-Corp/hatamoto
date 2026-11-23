package wpimport

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

var testVCMedia = map[string]Media{
	"12": {ID: "12", URL: "https://example.com/wp-content/uploads/2020/01/photo.jpg", Alt: "A photo"},
}

// vcRendered is a WPBakery page as the REST API renders it: shortcodes left in
// the text, wrapped in paragraphs, attribute quotes texturized.
const vcRendered = `<p>[vc_row][vc_column][vc_custom_heading text=&#8221;Example &amp; heading&#8221; font_container=&#8221;tag:h1|text_align:center&#8221;][vc_column_text]</p>
<p>Body text <a href="https://example.com/x">link</a>.</p>
<p>[/vc_column_text][vc_single_image image=&#8221;12&#8243; img_size=&#8221;full&#8221; alignment=&#8221;center&#8221;][vc_text_separator title=&#8221;Section title&#8221;][vc_btn title=&#8221;Contact&#8221; link=&#8221;url:%2Fcontact%2F|title:Contact&#8221;][vc_empty_space][/vc_column][/vc_row]</p>`

// vcRaw is the same page as typed (WXR, edit context).
const vcRaw = `[vc_row][vc_column][vc_custom_heading text="Example &amp; heading" font_container="tag:h1|text_align:center"][vc_column_text]

Body text <a href="https://example.com/x">link</a>.

[/vc_column_text][vc_single_image image="12" img_size="full" alignment="center"][vc_text_separator title="Section title"][vc_btn title="Contact" link="url:%2Fcontact%2F|title:Contact"][vc_empty_space][/vc_column][/vc_row]`

var betweenTagsRe = regexp.MustCompile(`>\s+<`)

func normHTML(s string) string { return betweenTagsRe.ReplaceAllString(strings.TrimSpace(s), "><") }

func TestVCAttrs(t *testing.T) {
	cases := map[string]struct {
		in   string
		want map[string]string
	}{
		"straight":   {` text="Hello" size='big' id=7`, map[string]string{"text": "Hello", "size": "big", "id": "7"}},
		"texturized": {` text=&#8221;Hello&#8221; alignment=&#8221;center&#8221;`, map[string]string{"text": "Hello", "alignment": "center"}},
		"inch":       {` size=&#8221;10&#8243; x=&#8221;1&#8221;`, map[string]string{"size": "10", "x": "1"}},
		"unicode":    {" text=\u201dHi\u201d w=\u201c5\u2033", map[string]string{"text": "Hi", "w": "5"}},
		"br":         {` a="1"<br />b="2"`, map[string]string{"a": "1", "b": "2"}},
		"upper+dup":  {` Key="a" key="b"`, map[string]string{"key": "b"}},
		"entity":     {` text=&#8221;A &amp; B&#8221;`, map[string]string{"text": "A &amp; B"}},
	}
	for name, tc := range cases {
		if got := vcAttrs(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
}

func TestMediaByIDAndWithMedia(t *testing.T) {
	m := MediaByID([]Media{{ID: "1", URL: "u1"}, {ID: "2", URL: "u2"}})
	if len(m) != 2 || m["2"].URL != "u2" {
		t.Fatalf("index: %v", m)
	}
	var o convertOpts
	WithMedia(m)(&o)
	if len(o.media) != 2 {
		t.Errorf("WithMedia did not set the index")
	}
}

func TestConvertWPBakeryRendered(t *testing.T) {
	c := Convert(vcRendered, false, WithMedia(testVCMedia))
	for _, want := range []string{
		`<h1 class="has-text-align-center">Example &amp; heading</h1>`,
		`<img src="https://example.com/wp-content/uploads/2020/01/photo.jpg" alt="A photo" class="aligncenter"/>`,
		`<h3>Section title</h3>`,
		`<a href="/contact/">Contact</a>`,
		`<a href="https://example.com/x">link</a>`,
	} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("missing %q in %s", want, c.HTML)
		}
	}
	if strings.Contains(c.HTML, "[vc_") || strings.Contains(c.HTML, "[/vc_") {
		t.Errorf("shortcode left: %s", c.HTML)
	}
	if regexp.MustCompile(`<p>\s*</p>`).MatchString(c.HTML) {
		t.Errorf("empty paragraph left: %s", c.HTML)
	}
	if len(c.Shortcodes) != 0 || len(c.MissingImages) != 0 || len(c.Dropped) != 0 {
		t.Errorf("converted tags must not be reported: %+v", c)
	}
	if c.WPBakery == 0 {
		t.Error("WPBakery count not set")
	}
	clean := entity.SanitizeRichText(c.HTML)
	for _, want := range []string{"<h1", "<img", `<a href="/contact/">`} {
		if !strings.Contains(clean, want) {
			t.Errorf("sanitiser dropped %q: %s", want, clean)
		}
	}
	if again := Convert(vcRendered, false, WithMedia(testVCMedia)); !reflect.DeepEqual(c, again) {
		t.Error("Convert is not deterministic")
	}
}

func TestConvertWPBakeryRawMatchesRendered(t *testing.T) {
	rendered := Convert(vcRendered, false, WithMedia(testVCMedia))
	raw := Convert(vcRaw, true, WithMedia(testVCMedia))
	if strings.Contains(raw.HTML, "[vc_") {
		t.Errorf("shortcode left: %s", raw.HTML)
	}
	if normHTML(raw.HTML) != normHTML(rendered.HTML) {
		t.Errorf("raw differs from rendered:\nraw:      %s\nrendered: %s", normHTML(raw.HTML), normHTML(rendered.HTML))
	}
}

func TestConvertWPBakeryHandlers(t *testing.T) {
	c := Convert(`[vc_single_image image="99"][vc_foo][vc_custom_heading text="Plain"][vc_separator][vc_btn title="Go"][vc_custom_heading text="T" font_container="tag:div" link="url:https%3A%2F%2Fexample.com%2F"]`, true, WithMedia(testVCMedia))
	if !reflect.DeepEqual(c.MissingImages, []string{"99"}) {
		t.Errorf("MissingImages = %v", c.MissingImages)
	}
	if !reflect.DeepEqual(c.Shortcodes, []string{"vc_foo"}) {
		t.Errorf("Shortcodes = %v", c.Shortcodes)
	}
	for _, want := range []string{"<h2>Plain</h2>", "<hr/>", "<p>Go</p>", `<p><a href="https://example.com/">T</a></p>`} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("missing %q in %s", want, c.HTML)
		}
	}
	if strings.Contains(c.HTML, "<img") || strings.Contains(c.HTML, "[vc_") {
		t.Errorf("unexpected: %s", c.HTML)
	}
	if got := Convert(`[vc_single_image image="12"]`, true); strings.Contains(got.HTML, "<img") || !reflect.DeepEqual(got.MissingImages, []string{"12"}) {
		t.Errorf("no media option: %+v", got)
	}
}

func TestConvertWithoutWPBakeryUnchanged(t *testing.T) {
	in := "<p>One</p><p></p><p>[gallery ids=\"1\"]</p><p>Two [sic]</p>"
	if got, want := Convert(in, false, WithMedia(testVCMedia)), Convert(in, false); !reflect.DeepEqual(got, want) {
		t.Errorf("options changed the output: %+v vs %+v", got, want)
	}
	if got := Convert(in, false); !strings.Contains(got.HTML, "<p></p>") {
		t.Errorf("empty paragraph cleanup must be off without WPBakery: %s", got.HTML)
	}
}
