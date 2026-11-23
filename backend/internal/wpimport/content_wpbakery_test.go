package wpimport

import (
	"encoding/base64"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
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
		"straight":        {` text="Hello" size='big' id=7`, map[string]string{"text": "Hello", "size": "big", "id": "7"}},
		"texturized":      {` text=&#8221;Hello&#8221; alignment=&#8221;center&#8221;`, map[string]string{"text": "Hello", "alignment": "center"}},
		"inch":            {` size=&#8221;10&#8243; x=&#8221;1&#8221;`, map[string]string{"size": "10", "x": "1"}},
		"unicode":         {" text=\u201dHi\u201d w=\u201c5\u2033", map[string]string{"text": "Hi", "w": "5"}},
		"br":              {` a="1"<br />b="2"`, map[string]string{"a": "1", "b": "2"}},
		"upper+dup":       {` Key="a" key="b"`, map[string]string{"key": "b"}},
		"curly inside":    {" text=\"Our \u201cbest\u201d offer\" w=1", map[string]string{"text": "Our \u201cbest\u201d offer", "w": "1"}},
		"curly in entity": {" text=&#8221;Our \u201cbest\u201d offer&#8221; w=1", map[string]string{"text": "Our \u201cbest\u201d offer", "w": "1"}},
		"backticks":       {" text=\"Why ``our``\"", map[string]string{"text": `Why "our"`}},
		"apostrophe":      {` text="It's ok" b='say "hi"'`, map[string]string{"text": "It's ok", "b": `say "hi"`}},
		"entity":          {` text=&#8221;A &amp; B&#8221;`, map[string]string{"text": "A &amp; B"}},
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

// vcEncode is how WPBakery stores raw HTML and map embeds.
func vcEncode(s string) string { return base64.StdEncoding.EncodeToString([]byte(url.QueryEscape(s))) }

func TestConvertWPBakeryRawHTML(t *testing.T) {
	payload := vcEncode(`<p>Hello <strong>there</strong></p>[gallery_plugin id="1"]<script>x()</script>`)
	for name, in := range map[string]string{
		"plain":      `<p>[vc_raw_html]` + payload + `[/vc_raw_html]</p>`,
		"wrapped":    `[vc_raw_html]<br />` + payload[:20] + "\n" + payload[20:] + `<br />[/vc_raw_html]`,
		"texturized": `[vc_raw_html]` + payload + `[/vc_raw_html]`,
	} {
		c := Convert(in, name != "plain")
		if !strings.Contains(c.HTML, "<strong>there</strong>") || strings.Contains(c.HTML, "<script") || strings.Contains(c.HTML, "x()") {
			t.Errorf("%s: %s", name, c.HTML)
		}
		if !reflect.DeepEqual(c.Shortcodes, []string{"gallery_plugin"}) {
			t.Errorf("%s: Shortcodes = %v", name, c.Shortcodes)
		}
	}
	// wptexturize turns "x" between digits into a multiplication sign.
	var p2, text string
	for n := 0; !strings.Contains(p2, "x"); n++ { // a payload whose base64 has an "x"
		text = "<p>" + string(rune('a'+n%26)) + string(rune('a'+n/26%26)) + string(rune('a'+n/676%26)) + " boards</p>"
		p2 = vcEncode(text)
		if n > 20000 {
			t.Fatal("no payload with an x found")
		}
	}
	i := strings.Index(p2, "x")
	c := Convert("[vc_raw_html]"+p2[:i]+"&#215;"+p2[i+1:]+"[/vc_raw_html]", false)
	if !strings.Contains(c.HTML, " boards") || strings.Contains(c.HTML, "[vc_") {
		t.Errorf("texturized payload: %s", c.HTML)
	}
	if c := Convert("[vc_raw_html]!!not base64!![/vc_raw_html]", false); !reflect.DeepEqual(c.Dropped, []string{"WPBakery raw HTML (unreadable)"}) || strings.Contains(c.HTML, "base64") {
		t.Errorf("unreadable: %+v", c)
	}
}

func TestConvertWPBakeryRawJSAndSlider(t *testing.T) {
	js := vcEncode(`<script>alert(1)</script>`)
	c := Convert(`<p>Before</p>[vc_raw_js]`+js+`[/vc_raw_js][vc_posts_slider count="3"]<p>After</p>`, false)
	if !reflect.DeepEqual(c.Shortcodes, []string{"vc_posts_slider", "vc_raw_js"}) {
		t.Errorf("Shortcodes = %v", c.Shortcodes)
	}
	if strings.Contains(c.HTML, "alert") || strings.Contains(c.HTML, "<script") || strings.Contains(c.HTML, js) {
		t.Errorf("script kept: %s", c.HTML)
	}
	if !strings.Contains(c.HTML, "Before") || !strings.Contains(c.HTML, "After") {
		t.Errorf("text lost: %s", c.HTML)
	}
}

func TestConvertWPBakeryMaps(t *testing.T) {
	ok := Convert(`[vc_gmaps link="#E-8_`+vcEncode(`<iframe src="https://www.google.com/maps/embed?pb=!1m18&amp;x=1" width="600"></iframe>`)+`"]`, true)
	if want := `<a href="https://www.google.com/maps/embed?pb=!1m18&amp;x=1">View on Google Maps</a>`; !strings.Contains(ok.HTML, want) {
		t.Errorf("missing %q in %s", want, ok.HTML)
	}
	for _, bad := range []string{
		`<iframe src="http://www.google.com/maps/embed?pb=1"></iframe>`,
		`<iframe src="https://maps.example.net/maps/embed"></iframe>`,
		`<iframe src="https://www.google.com/other"></iframe>`,
		`https://evil.example/maps`,
	} {
		c := Convert(`[vc_gmaps link="#E-8_`+vcEncode(bad)+`"]`, true)
		if strings.Contains(c.HTML, "<a ") || !reflect.DeepEqual(c.Dropped, []string{"Google Maps (unrecognised link)"}) {
			t.Errorf("%s: %+v", bad, c)
		}
	}
}

func TestConvertWPBakeryLimits(t *testing.T) {
	big := vcEncode(strings.Repeat("a", maxRawHTMLDecoded+1))
	c := Convert(`[vc_raw_html]`+big+`[/vc_raw_html]<p>kept</p>`, false)
	if !reflect.DeepEqual(c.Dropped, []string{"WPBakery raw HTML (too large)"}) || strings.Contains(c.HTML, "aaaa") || !strings.Contains(c.HTML, "kept") {
		t.Errorf("raw html cap: %+v", c)
	}
	// Each block is within its cap, together they exceed the body budget.
	part := vcEncode("<p>" + strings.Repeat("b", maxRawHTMLDecoded-16) + "</p>")
	c = Convert(strings.Repeat(`[vc_raw_html]`+part+`[/vc_raw_html]`, 5), false)
	if len(c.Dropped) == 0 || c.Dropped[0] != "WPBakery raw HTML (too large)" {
		t.Errorf("body cap: %v", c.Dropped)
	}
	deep := strings.Repeat("[vc_row]", maxVCDepth+1) + "deep text" + strings.Repeat("[/vc_row]", maxVCDepth+1)
	c = Convert(deep, true)
	if !reflect.DeepEqual(c.Dropped, []string{"WPBakery (nesting too deep)"}) || strings.Contains(c.HTML, "[vc_") || !strings.Contains(c.HTML, "deep text") {
		t.Errorf("depth cap: %+v", c)
	}
	many := strings.Repeat("[vc_empty_space]", maxVCTags+10) + "tail"
	c = Convert(many, true)
	if !reflect.DeepEqual(c.Dropped, []string{"WPBakery (too many shortcodes)"}) || strings.Contains(c.HTML, "[vc_") || !strings.Contains(c.HTML, "tail") {
		t.Errorf("tag cap: %v %.60s", c.Dropped, c.HTML)
	}
	if !reflect.DeepEqual(Convert(many, true), c) {
		t.Error("Convert is not deterministic")
	}
}

func TestConvertWPBakeryColumnsStaySeparate(t *testing.T) {
	cols := `[vc_row][vc_column width="1/2"][vc_column_text]Left text.[/vc_column_text][/vc_column][vc_column width="1/2"][vc_column_text]Right text.[/vc_column_text][/vc_column][/vc_row]`
	for name, tc := range map[string]struct {
		in  string
		raw bool
	}{"raw": {cols, true}, "rendered": {"<p>" + cols + "</p>", false}} {
		c := Convert(tc.in, tc.raw)
		if normHTML(c.HTML) != "<p>Left text.</p><p>Right text.</p>" {
			t.Errorf("%s: columns ran together or left empty paragraphs: %s", name, c.HTML)
		}
	}
}

func TestConvertWPBakeryTabTitlesBecomeHeadings(t *testing.T) {
	in := `[vc_tta_accordion][vc_tta_section title="How long does it take?" tab_id="a"][vc_column_text]About two days.[/vc_column_text][/vc_tta_section][vc_tta_section title="Q &amp; A"][vc_column_text]Yes.[/vc_column_text][/vc_tta_section][/vc_tta_accordion]`
	for name, tc := range map[string]struct {
		in  string
		raw bool
	}{"raw": {in, true}, "rendered": {"<p>" + in + "</p>", false}} {
		c := Convert(tc.in, tc.raw)
		if got, want := normHTML(c.HTML), "<h3>How long does it take?</h3><p>About two days.</p><h3>Q &amp; A</h3><p>Yes.</p>"; got != want {
			t.Errorf("%s: got %s want %s", name, got, want)
		}
	}
}

func TestConvertWPBakeryUnbalancedTagsRecover(t *testing.T) {
	var b strings.Builder
	for i := range 40 {
		closer := "" // the closer of every row's text block is missing
		b.WriteString(`[vc_row][vc_column][vc_column_text]Row ` + strconv.Itoa(i) + closer + `[vc_custom_heading text="Heading ` + strconv.Itoa(i) + `"][/vc_column][/vc_row]`)
	}
	b.WriteString("[/vc_column][/vc_nothing]")
	c := Convert(b.String(), true)
	for i := range 40 {
		if want := "<h2>Heading " + strconv.Itoa(i) + "</h2>"; !strings.Contains(c.HTML, want) {
			t.Fatalf("missing %q: %.200s", want, c.HTML)
		}
	}
	if len(c.Dropped) != 0 || strings.Contains(c.HTML, "[") {
		t.Errorf("dropped=%v html=%.200s", c.Dropped, c.HTML)
	}
}

func TestConvertWPBakeryQuotesInsideValues(t *testing.T) {
	raw := Convert("[vc_custom_heading text=\"Our \u201cbest\u201d offer\"]", true)
	rendered := Convert("<p>[vc_custom_heading text=&#8221;Our \u201cbest\u201d offer&#8221;]</p>", false)
	want := "<h2>Our \u201cbest\u201d offer</h2>"
	if raw.HTML != want || rendered.HTML != want {
		t.Errorf("raw %q rendered %q want %q", raw.HTML, rendered.HTML, want)
	}
}
