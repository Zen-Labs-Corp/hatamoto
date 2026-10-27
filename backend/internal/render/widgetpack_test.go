package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const packTestKind = "pp_w"

func readIf(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return b
}

// TestPackCorpus runs every testdata/packs/<case>: the "want" file holds the
// rule that must appear in the error, empty for a pack that must pass.
func TestPackCorpus(t *testing.T) {
	dirs, err := filepath.Glob("testdata/packs/*")
	if err != nil || len(dirs) < 28 {
		t.Fatalf("corpus: %d cases, %v", len(dirs), err)
	}
	for _, d := range dirs {
		t.Run(filepath.Base(d), func(t *testing.T) {
			f := PackFiles{HTML: readIf(t, d+"/widget.html"), CSS: readIf(t, d+"/widget.css"), Island: readIf(t, d+"/island.js")}
			want := strings.TrimSpace(string(readIf(t, d+"/want")))
			err := ValidatePackWidget(packTestKind, f)
			switch {
			case want == "" && err != nil:
				t.Fatalf("good pack refused: %v", err)
			case want != "" && err == nil:
				t.Fatalf("hostile pack accepted, want rule %q", want)
			case want != "" && !strings.Contains(err.Error(), want):
				t.Fatalf("err = %v, want rule %q", err, want)
			}
		})
	}
}

func TestPackEscapedPropValuesDoNotTripScan(t *testing.T) {
	// A prop value holding <script> is escaped by html/template; only markup
	// the template itself writes is judged. The sample values here are fixed,
	// but the template must still pass when it prints props in every context.
	html := `<div {{.Attrs}} title="{{.P.title}}"><a href="{{.P.url}}">{{.P.text}}</a>{{.Children}}</div>`
	if err := ValidatePackWidget(packTestKind, PackFiles{HTML: []byte(html)}); err != nil {
		t.Fatal(err)
	}
}

func TestScopeCheckCSS(t *testing.T) {
	for css, ok := range map[string]bool{
		`.hm-pp_w{}`:                           true,
		`.hm-pp_w:hover .y{color:red}`:         true,
		`.hm-pp_w.on{}`:                        true,
		`.hm-pp_w[data-x="a,b"]{}`:             true,
		`.hm-pp_w:nth-child(2n+1){}`:           true,
		`@media (max-width:600px){.hm-pp_w{}}`: true,
		`.hm-pp_w .a,.hm-pp_w .b{}`:            true,
		`body{}`:                               false,
		`.hm-pp_w,body{}`:                      false,
		`.hm-pp_wx{}`:                          false,
		`.hm-pp_w-x{}`:                         false,
		`.hm-pp_w_x{}`:                         false,
		`.hm-pp_w~.x{}`:                        false,
		`@media x{@media y{.hm-pp_w{}}}`:       false,
		`@media x{body{}}`:                     false,
		`.hm-pp_w{}.other{}`:                   false,
	} {
		err := scopeCheckCSS(packTestKind, css)
		if (err == nil) != ok {
			t.Errorf("scopeCheckCSS(%q) = %v, want ok=%v", css, err, ok)
		}
	}
}

func TestPackRejectsNonPackKind(t *testing.T) {
	for _, k := range []string{"stat", "a_b_c", ""} {
		if err := ValidatePackWidget(k, PackFiles{HTML: []byte(`<div {{.Attrs}}></div>`)}); err == nil {
			t.Errorf("kind %q accepted", k)
		}
	}
}

// Whatever node the cap trips on (an empty {{else}} is a typed-nil list), the
// validator must answer with a rule, not panic.
func TestPackNodeCapAtEmptyElse(t *testing.T) {
	for n := 100; n < 400; n++ {
		src := "<div {{.Attrs}}>" + strings.Repeat("{{.P.a}}", n%9) + strings.Repeat("{{if .P.a}}{{range .P.b}}x{{end}}{{end}}", n) + "</div>"
		err := ValidatePackWidget(packTestKind, PackFiles{HTML: []byte(src)})
		if err != nil && strings.Contains(err.Error(), "internal") {
			t.Fatalf("n=%d: validator panicked: %v", n, err)
		}
		if n == 399 && (err == nil || !strings.Contains(err.Error(), "complexity")) {
			t.Fatalf("n=%d: err = %v, want complexity", n, err)
		}
	}
}
