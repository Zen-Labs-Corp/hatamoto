package render

import (
	"bytes"
	"encoding/json"
	"html/template"
	"os"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
)

func TestFuncsMarkers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  template.HTMLAttr
		want string
	}{
		{"section", sectionMarker(preparedSection{ID: "s1", Type: "faq"}), `id="s1" data-hm-section="s1" data-hm-type="faq"`},
		{"section reserved id", sectionMarker(Section{ID: "main", Type: "hero"}), `data-hm-section="main" data-hm-type="hero"`},
		{"section uuid-like id", sectionMarker(Section{ID: "3f2a-UPPER", Type: "hero"}), `data-hm-section="3f2a-UPPER" data-hm-type="hero"`},
		{"section ptr value", sectionMarker(Section{ID: "a", Type: "hero"}), `id="a" data-hm-section="a" data-hm-type="hero"`},
		{"section escapes", sectionMarker(Section{ID: `"><x`, Type: "t"}), `data-hm-section="&#34;&gt;&lt;x" data-hm-type="t"`},
		{"field", fieldMarker("faqs", 2, "answer"), `data-hm-field="faqs.2.answer"`},
		{"field single", fieldMarker("headline"), `data-hm-field="headline"`},
		{"field escapes", fieldMarker(`a"b`), `data-hm-field="a&#34;b"`},
		{"entity", entityMarker("e1"), `data-hm-entity="e1"`},
		{"entity empty", entityMarker(""), ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if string(tt.got) != tt.want {
				t.Fatalf("got %q want %q", tt.got, tt.want)
			}
		})
	}
}

func TestFuncsInTemplate(t *testing.T) {
	t.Parallel()
	src := `<section {{hmsec .S}}><h2 {{hmf "faqs" 0 "q"}}>{{.Q}}</h2><a href="{{cta .U}}">x</a></section>`
	tpl := template.Must(template.New("t").Funcs(baseFuncs()).Parse(src))
	var b bytes.Buffer
	data := map[string]any{
		"S": preparedSection{Section: Section{ID: "s1", Type: "faq"}},
		"Q": `<script>alert(1)</script>`,
		"U": "javascript:alert(1)",
	}
	if err := tpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
	want := `<section id="s1" data-hm-section="s1" data-hm-type="faq"><h2 data-hm-field="faqs.0.q">&lt;script&gt;alert(1)&lt;/script&gt;</h2><a href="#">x</a></section>`
	if b.String() != want {
		t.Fatalf("got  %s\nwant %s", b.String(), want)
	}
}

func TestFuncsSafeURLAndImgSrc(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, cta, img string }{
		{"https://a.com/x", "https://a.com/x", "https://a.com/x"},
		{"/relative", "/relative", "/relative"},
		{"#top", "#top", "#top"},
		{"mailto:a@b.c", "mailto:a@b.c", ""},
		{"tel:+1555", "tel:+1555", ""},
		{"javascript:alert(1)", "#", ""},
		{" JaVaScRiPt:alert(1)", "#", ""},
		{"//evil.com", "#", ""},
		{"data:text/html,x", "#", ""},
		{"data:image/png;base64,AAAA", "#", "data:image/png;base64,AAAA"},
		{"data:image/svg+xml;base64,AAAA", "#", ""},
		{"", "#", ""},
	}
	for _, tt := range tests {
		if got := safeURL(tt.in); got != tt.cta {
			t.Errorf("safeURL(%q)=%q want %q", tt.in, got, tt.cta)
		}
		if got := imgSrc(tt.in); got != tt.img && !(tt.in == "mailto:a@b.c" || tt.in == "tel:+1555") {
			t.Errorf("imgSrc(%q)=%q want %q", tt.in, got, tt.img)
		}
	}
}

func TestHelperFuncs(t *testing.T) {
	// Not parallel: swaps the package clock.
	nowFunc = func() time.Time { return time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC) }
	defer func() { nowFunc = time.Now }()
	tpl := template.Must(template.New("t").Funcs(baseFuncs()).Parse(
		`{{year}}|{{join (list "a" "b") ", "}}|{{range seq 3}}{{.}}{{end}}|{{default "d" ""}}|{{default "d" "v"}}|{{(dict "k" "v").k}}|{{hasPrefix "abc" "ab"}}`))
	var b strings.Builder
	if err := tpl.Execute(&b, nil); err != nil {
		t.Fatal(err)
	}
	if want := "2031|a, b|012|d|v|v|true"; b.String() != want {
		t.Fatalf("got %q want %q", b.String(), want)
	}
	if _, err := dict("odd"); err == nil {
		t.Fatal("dict must reject odd args")
	}
	if len(seq(1_000_000)) != maxSeq {
		t.Fatal("seq must be capped")
	}
}

func TestSafeHrefKeepsTelAndSMSInTemplates(t *testing.T) {
	t.Parallel()
	tpl := template.Must(template.New("t").Funcs(baseFuncs()).Parse(
		`<a href="{{safeHref .Tel}}">t</a><a href="{{cta .SMS}}">s</a><a href="{{cta .JS}}">j</a><a href="{{telHref .Phone}}">p</a>`))
	var b strings.Builder
	err := tpl.Execute(&b, map[string]string{
		"Tel": "tel:+15550100", "SMS": "sms:+15550100", "JS": "javascript:alert(1)", "Phone": "+1 (555) 010-0",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `<a href="tel:&#43;15550100">t</a><a href="sms:&#43;15550100">s</a><a href="#">j</a><a href="tel:&#43;15550100">p</a>`
	if b.String() != want {
		t.Fatalf("got  %s\nwant %s", b.String(), want)
	}
	if strings.Contains(b.String(), "ZgotmplZ") {
		t.Fatal("href rewritten to #ZgotmplZ")
	}
}

func TestEmbedAllowed(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{
		"https://www.google.com/maps/embed?pb=abc":                 true,
		"https://www.openstreetmap.org/export/embed.html?bbox=1,2": true,
		"https://www.youtube.com/embed/abc":                        true,
		"http://www.google.com/maps/embed?pb=abc":                  false,
		"https://www.google.com/maps/place/x":                      false,
		"https://evil.example/https://www.google.com/maps/embed":   false,
		"javascript:alert(1)":                                      false,
		"":                                                         false,
	} {
		if got := embedAllowed(in); got != want {
			t.Errorf("embedAllowed(%q)=%v want %v", in, got, want)
		}
	}
}

func TestHighlight(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, lang, src string
		want, notWant   string
	}{
		{"empty", "go", "", "", ""},
		{"escapes script", "html", `<script>alert(1)</script>`, `&lt;script&gt;alert(1)&lt;/script&gt;`, `<script>`},
		{"string", "go", `x := "a<b"`, `<span class="hm-tok hm-tok--s">&#34;a&lt;b&#34;</span>`, `a<b`},
		{"go comment", "go", "// hi <b>", `<span class="hm-tok hm-tok--c">// hi &lt;b&gt;</span>`, `<b>`},
		{"hash comment", "sh", "# note", `hm-tok--c`, ``},
		{"hash not go", "go", "#x", `#x`, `hm-tok`},
		{"keyword", "go", "func main", `<span class="hm-tok hm-tok--k">func</span> main`, ``},
		{"html comment", "html", "<!-- c -->", `hm-tok--c`, `<!--`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := string(highlight(tt.lang, tt.src))
			if !strings.Contains(got, tt.want) || (tt.notWant != "" && strings.Contains(got, tt.notWant)) {
				t.Fatalf("highlight(%q,%q) = %q; want contains %q, not %q", tt.lang, tt.src, got, tt.want, tt.notWant)
			}
		})
	}
}

func TestCodeKindAccepted(t *testing.T) {
	t.Parallel()
	if _, _, _, err := parseSectionSchema([]byte(`{"type":"object","properties":{"c":{"type":"string","x-hm-field":"code"}}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestHighlightRendersCSSValueAsEscapedText(t *testing.T) {
	t.Parallel()
	got := string(highlight("css", template.CSS(`a>b{content:"\3c "}`)))
	if !strings.Contains(got, "a&gt;b{content:") || strings.Contains(got, "a>b") {
		t.Fatalf("highlight of template.CSS = %q; want escaped text", got)
	}
}

func TestSanitisedHTML(t *testing.T) {
	t.Parallel()
	got := string(sanitisedHTML(`<p data-hm-x="1">hi</p><script>alert(1)</script>`))
	if strings.Contains(got, "<script") || strings.Contains(got, "data-hm-") || !strings.Contains(got, "<p>hi</p>") {
		t.Errorf("got %q", got)
	}
	for _, v := range []any{nil, 42, 1.5} {
		if got := sanitisedHTML(v); got != "" {
			t.Errorf("sanitisedHTML(%v) = %q", v, got)
		}
	}
}

func TestFormatDate(t *testing.T) {
	t.Parallel()
	d := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	for name, in := range map[string]any{"time": d, "ptr": &d, "rfc3339": "2026-10-13T09:00:00Z", "date": "2026-10-13"} {
		if got := formatDate(in, "2 January 2006"); got != "13 October 2026" {
			t.Errorf("%s: got %q", name, got)
		}
	}
	var nilPtr *time.Time
	for _, in := range []any{time.Time{}, nilPtr, "nope", "", nil, 42} {
		if got := formatDate(in, "2 January 2006"); got != "" {
			t.Errorf("formatDate(%v) = %q, want empty", in, got)
		}
	}
}

func TestReadingTime(t *testing.T) {
	t.Parallel()
	words := func(n int) string { return "<p>" + strings.TrimSpace(strings.Repeat("word ", n)) + "</p>" }
	for n, want := range map[int]int{0: 0, 1: 1, 200: 1, 201: 2, 401: 3} {
		if got := readingTime(words(n)); got != want {
			t.Errorf("%d words: got %d, want %d", n, got, want)
		}
	}
	if got := readingTime("<p>a</p><p>b</p>"); got != 1 {
		t.Errorf("adjacent blocks: got %d", got)
	}
	if got := readingTime(nil); got != 0 {
		t.Errorf("nil: got %d", got)
	}
}

func TestFormatDateTimezone(t *testing.T) {
	t.Parallel()
	late := time.Date(2026, 10, 13, 22, 0, 0, 0, time.UTC) // 14 Oct 09:00 in Sydney (AEDT)
	if got := formatDate(late, "2 January 2006"); got != "13 October 2026" {
		t.Errorf("default keeps the value's zone, got %q", got)
	}
	if got := formatDate(late, "2 January 2006", "Australia/Sydney"); got != "14 October 2026" {
		t.Errorf("Sydney = %q, want 14 October 2026", got)
	}
	if got := formatDate("2026-10-13T22:00:00Z", "2 January 2006", "Australia/Sydney"); got != "14 October 2026" {
		t.Errorf("string input in Sydney = %q", got)
	}
	for _, tz := range []string{"", "Not/AZone"} {
		if got := formatDate(late, "2 January 2006", tz); got != "13 October 2026" {
			t.Errorf("tz %q must fall back to the value's zone, got %q", tz, got)
		}
	}
}

func TestExtraFuncs(t *testing.T) {
	run := func(src string, data any) string {
		t.Helper()
		var b strings.Builder
		tpl := template.Must(template.New("t").Funcs(baseFuncs()).Parse(src))
		if err := tpl.Execute(&b, data); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return b.String()
	}
	for _, c := range []struct{ src, want string }{
		{`{{add 2 3}} {{sub 2 3}} {{mul 4 5}} {{div 7 2}} {{mod 7 3}}`, "5 -1 20 3 1"},
		{`{{div 7 0}} {{mod 7 0}} {{div "x" 2}}`, "0 0 0"},
		{`{{add .F 1}} {{add .S 1}} {{add .N 1}}`, "3 11 1"},
		{`{{mul 4000000000 4000000000}}`, "4611686014132420609"}, // clamped inputs, no wrap
		{`{{lower "AbC"}}{{upper "AbC"}}`, "abcABC"},
		{`{{contains "hello" "ell"}} {{contains "hello" "z"}} {{hasSuffix "a.png" ".png"}}`, "true false true"},
		{`{{replace "a-b-c" "-" " "}}`, "a b c"},
		{`{{range split "a,b,c" ","}}[{{.}}]{{end}}`, "[a][b][c]"},
		{`{{trim "  x "}}|{{trimPrefix "/a/b" "/a"}}|{{trimSuffix "a.html" ".html"}}`, "x|/b|a"},
		{`{{atoi "42"}} {{atoi " 7 "}} {{atoi "nope"}} {{atoi nil}} {{atoi 3.9}}`, "42 7 0 0 3"},
		{`{{with fromJSON .J}}{{.a}} {{index .b 1}}{{end}}`, "1 y"},
		{`{{fromJSON "{bad"}}|{{fromJSON ""}}|{{fromJSON 5}}`, "||5"},
	} {
		got := run(c.src, map[string]any{"F": 2.0, "S": "10", "N": nil, "J": `{"a":1,"b":["x","y"]}`})
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.src, got, c.want)
		}
	}
	// Results are escaped: no func returns trusted HTML.
	if got := run(`{{replace "a" "a" "<b>"}}`, nil); got != "&lt;b&gt;" {
		t.Errorf("replace result not escaped: %q", got)
	}
	// Oversized input decodes to nil.
	big := `"` + strings.Repeat("x", maxFromJSON) + `"`
	if fromJSON(big) != nil {
		t.Error("fromJSON accepted input over the cap")
	}
}

func TestSanitisedSectionHTML(t *testing.T) {
	t.Parallel()
	in := `<div class="a" id="b" style="color:red" data-x="1" aria-label="l" onclick="x()"><script>x()</script><details><summary>s</summary>t</details><iframe src="https://evil.example/x"></iframe></div>`
	var b strings.Builder
	tpl := template.Must(template.New("t").Funcs(baseFuncs()).Parse(`{{sanitisedSectionHTML .}}|{{sanitisedHTML .}}|{{sanitisedSectionHTML 5}}`))
	if err := tpl.Execute(&b, in); err != nil {
		t.Fatal(err)
	}
	section, rich, _ := strings.Cut(b.String(), "|")
	for _, want := range []string{`class="a"`, `id="b"`, `style="color:red"`, `data-x="1"`, `aria-label="l"`, "<details>"} {
		if !strings.Contains(section, want) {
			t.Errorf("section policy lost %s: %s", want, section)
		}
	}
	for _, bad := range []string{"onclick", "<script", "evil.example"} {
		if strings.Contains(section, bad) {
			t.Errorf("section policy kept %s: %s", bad, section)
		}
	}
	if strings.Contains(rich, `id="b"`) || strings.Contains(rich, "style=") {
		t.Errorf("rich-text policy must stay stricter: %s", rich)
	}
	if !strings.HasSuffix(b.String(), "|") {
		t.Errorf("non-string input = %q", b.String())
	}
}

// TestSafeURLAgreesWithAllowedInlineHref keeps the inline sanitiser's link rule
// (entity cannot import render) in step with safeURL.
func TestSafeURLAgreesWithAllowedInlineHref(t *testing.T) {
	raw, err := os.ReadFile("../entity/testdata/inline_href_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Href string `json:"href"`
		OK   bool   `json:"ok"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := safeURL(c.Href) != "#"; got != entity.AllowedInlineHref(c.Href) || got != c.OK {
			t.Errorf("%q: safeURL allows %v, AllowedInlineHref %v, fixture %v", c.Href, got, entity.AllowedInlineHref(c.Href), c.OK)
		}
	}
}
