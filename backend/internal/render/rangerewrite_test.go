package render

import (
	"bytes"
	"html/template"
	"iter"
	"strings"
	"testing"
	"time"
)

// rangeData builds fresh data per run: a channel can be drained only once.
func rangeData() any {
	ch := make(chan int, 3)
	for i := 1; i <= 3; i++ {
		ch <- i
	}
	close(ch)
	return map[string]any{
		"Items": []string{"a", "b", "c", "d", "e"},
		"Map":   map[string]int{"z": 1, "a": 2, "m": 3},
		"N":     4,
		"Nil":   []string(nil),
		"Seq":   iter.Seq[int](func(y func(int) bool) { _ = y(1) && y(2) && y(3) }),
		"Seq2":  iter.Seq2[string, int](func(y func(string, int) bool) { _ = y("k", 1) && y("l", 2) }),
		"Chan":  ch,
		"Big":   int64(1_000_000_000),
	}
}

func execRange(src string, rewrite bool) (string, error) {
	funcs := template.FuncMap{
		rangeBudgetFunc: standaloneRangeHandle,
		"rows":          func(n int) []string { return make([]string, n) },
	}
	root := template.New("x").Funcs(funcs)
	if _, err := root.Parse(src); err != nil {
		return "", err
	}
	if rewrite {
		rewritePluginRanges(root, "x", src, "acme")
	}
	var b bytes.Buffer
	err := root.Execute(&b, rangeData())
	return b.String(), err
}

func renderBoth(t *testing.T, src string) string {
	t.Helper()
	want, werr := execRange(src, false)
	got, gerr := execRange(src, true)
	if (werr == nil) != (gerr == nil) || (werr != nil && werr.Error() != gerr.Error()) {
		t.Fatalf("error differs: unrestricted %v, rewritten %v", werr, gerr)
	}
	if got != want {
		t.Fatalf("output differs:\nunrestricted %q\nrewritten    %q", want, got)
	}
	return got
}

func TestRangeRewriteKeepsSemantics(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"slice":        `{{range .Items}}[{{.}}]{{end}}`,
		"slice2":       `{{range $i, $s := .Items}}{{$i}}={{$s}};{{end}}`,
		"map":          `{{range $k, $v := .Map}}{{$k}}{{$v}};{{end}}`,
		"int":          `{{range $i := .N}}{{$i}}{{end}}`,
		"len":          `{{range $i := len (rows 3)}}{{$i}}{{end}}`,
		"seq":          `{{range .Seq}}{{.}}{{end}}`,
		"seq2":         `{{range $k, $v := .Seq2}}{{$k}}{{$v}}{{end}}`,
		"chan":         `{{range .Chan}}{{.}}{{end}}`,
		"nil":          `{{range .Nil}}x{{end}}`,
		"else":         `{{range .Nil}}x{{else}}none{{end}}`,
		"var field":    `{{$c := .}}{{range $c.Items}}{{.}}{{end}}`,
		"var":          `{{$svc := .Items}}{{range $i, $s := $svc}}{{$s}}{{end}}`,
		"if lt":        `{{range $i, $x := .Items}}{{if lt $i 3}}{{$x}}{{end}}{{end}}`,
		"nested":       `{{range $i := .N}}{{range $j := .N}}{{$i}}{{$j}},{{end}}{{end}}`,
		"break":        `{{range $i := .N}}{{if eq $i 2}}{{break}}{{end}}{{$i}}{{end}}`,
		"continue":     `{{range $i := .N}}{{if eq $i 2}}{{continue}}{{end}}{{$i}}{{end}}`,
		"in with":      `{{with .Items}}{{range .}}{{.}}{{end}}{{end}}`,
		"in if else":   `{{if false}}{{else}}{{range .Items}}{{.}}{{end}}{{end}}`,
		"in else list": `{{range .Nil}}{{else}}{{range .Items}}{{.}}{{end}}{{end}}`,
		"define":       `{{define "p"}}{{range .Items}}{{.}}{{end}}{{end}}{{template "p" .}}`,
		"href attr":    `<a href="/x/{{range .Items}}{{.}}{{end}}?q={{range .Items}}{{.}} {{end}}">y</a>`,
		"no range":     `<p>{{.N}}</p>`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			renderBoth(t, src)
		})
	}
}

func TestRangeRewriteKeepsRangeErrors(t *testing.T) {
	t.Parallel()
	const src = `{{range true}}x{{end}}`
	_, want := execRange(src, false)
	_, got := execRange(src, true)
	if want == nil || got == nil || want.Error() != got.Error() {
		t.Fatalf("errors differ: %v / %v", want, got)
	}
	if !strings.Contains(got.Error(), "range can't iterate over") {
		t.Fatalf("unexpected error %v", got)
	}
}

func TestRangeRewriteStopsAtBudget(t *testing.T) {
	t.Parallel()
	for name, src := range map[string]string{
		"huge int":   `{{range $i := 1000000000}}.{{end}}`,
		"int field":  `{{range .Big}}.{{end}}`,
		"nested":     `{{$n := 100000}}{{range $i := $n}}{{range $j := $n}}.{{end}}{{end}}`,
		"huge slice": `{{range $i := len (rows 3000000)}}.{{end}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			start := time.Now()
			got, err := execRange(src, true)
			if err != nil {
				t.Fatal(err)
			}
			// A nested loop's outer iteration also spends from the budget.
			if n := strings.Count(got, "."); n > pluginRangeBudget || n < pluginRangeBudget-1 {
				t.Fatalf("rendered %d iterations, want about %d", n, pluginRangeBudget)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("took %v", d)
			}
		})
	}
}

func TestRangeRewriteSkipsTreesWithoutRange(t *testing.T) {
	t.Parallel()
	const src = `<p>{{if .N}}a{{end}}</p>{{define "d"}}{{with .Items}}b{{end}}{{end}}`
	root := template.Must(template.New("x").Parse(src))
	before := root.Tree.Root.String() + root.Lookup("d").Tree.Root.String()
	rewritePluginRanges(root, "x", src, "acme")
	if after := root.Tree.Root.String() + root.Lookup("d").Tree.Root.String(); after != before {
		t.Fatalf("tree changed:\n%s\n%s", before, after)
	}
}

func TestRangeBudgetVarNotLexable(t *testing.T) {
	t.Parallel()
	for _, src := range []string{`{{$hm-budget}}`, `{{$hm-budget := 1}}`, `{{$hm-budget.Spend}}`} {
		if _, err := template.New("x").Parse(src); err == nil {
			t.Errorf("%s parsed; plugin source could name the budget", src)
		}
	}
}

func TestRangeRewriteSurvivesClone(t *testing.T) {
	t.Parallel()
	const src = `<a href="/{{range .Items}}{{.}}{{end}}" title="{{range .Items}}{{.}}{{end}}">{{range .Items}}<i>{{.}}</i>{{end}}</a>`
	root := template.New("x").Funcs(template.FuncMap{rangeBudgetFunc: standaloneRangeHandle})
	template.Must(root.Parse(src))
	rewritePluginRanges(root, "x", src, "acme")
	c, err := root.Clone()
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := c.Execute(&b, rangeData()); err != nil {
		t.Fatal(err)
	}
	want := `<a href="/abcde" title="abcde"><i>a</i><i>b</i><i>c</i><i>d</i><i>e</i></a>`
	if b.String() != want {
		t.Fatalf("got %q want %q", b.String(), want)
	}
}
