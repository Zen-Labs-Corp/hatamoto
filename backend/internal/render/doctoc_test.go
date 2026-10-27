package render

import (
	"html/template"
	"strings"
	"testing"
)

func TestDocBody(t *testing.T) {
	t.Parallel()
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		if got := docBody(""); got.HTML != "" || got.TOC != nil {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("no headings", func(t *testing.T) {
		t.Parallel()
		got := docBody("<p>Hi</p><h4>Deep</h4>")
		if got.HTML != "<p>Hi</p><h4>Deep</h4>" || len(got.TOC) != 0 {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("duplicates and levels", func(t *testing.T) {
		t.Parallel()
		got := docBody("<h2>Setup</h2><h3>Setup</h3><h2>Setup</h2><h2>!!!</h2>")
		want := []TOCItem{{"setup", "Setup", 2}, {"setup-2", "Setup", 3}, {"setup-3", "Setup", 2}, {"section", "!!!", 2}}
		if len(got.TOC) != len(want) {
			t.Fatalf("toc = %+v", got.TOC)
		}
		for i := range want {
			if got.TOC[i] != want[i] {
				t.Errorf("toc[%d] = %+v, want %+v", i, got.TOC[i], want[i])
			}
		}
		if !strings.Contains(string(got.HTML), `<h3 id="setup-2">Setup</h3>`) {
			t.Errorf("html = %s", got.HTML)
		}
	})
	t.Run("nested markup", func(t *testing.T) {
		t.Parallel()
		got := docBody("<h2>Use <code>navPos</code> now</h2>")
		if got.TOC[0].Text != "Use navPos now" || got.TOC[0].ID != "use-navpos-now" {
			t.Errorf("toc = %+v", got.TOC)
		}
		if !strings.Contains(string(got.HTML), `<h2 id="use-navpos-now">Use <code>navPos</code> now</h2>`) {
			t.Errorf("html = %s", got.HTML)
		}
	})
	t.Run("text is escaped", func(t *testing.T) {
		t.Parallel()
		got := docBody("<h2>a &lt;script&gt;x&lt;/script&gt;</h2>")
		if got.TOC[0].Text != "a <script>x</script>" {
			t.Fatalf("text = %q", got.TOC[0].Text)
		}
		if strings.Contains(string(got.HTML), "<script>") {
			t.Errorf("html = %s", got.HTML)
		}
		var b strings.Builder
		tpl := template.Must(template.New("t").Parse(`{{range .}}{{.Text}}{{end}}`))
		if err := tpl.Execute(&b, got.TOC); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), "<script>") {
			t.Errorf("toc output = %s", b.String())
		}
	})
	t.Run("reserved ids", func(t *testing.T) {
		t.Parallel()
		got := docBody("<h2>Top</h2><h2>TOC</h2>")
		if got.TOC[0].ID != "top-2" || got.TOC[1].ID != "toc-2" {
			t.Errorf("toc = %+v", got.TOC)
		}
	})
	t.Run("existing id kept", func(t *testing.T) {
		t.Parallel()
		got := docBody(`<h2 id="mine">A</h2><h2>Mine</h2>`)
		if got.TOC[0].ID != "mine" || got.TOC[1].ID != "mine-2" {
			t.Errorf("toc = %+v", got.TOC)
		}
	})
	t.Run("round trip keeps markup", func(t *testing.T) {
		t.Parallel()
		in := `<p>See <a class="y" href="/x">link</a>.</p><pre><code class="language-go">a &lt; b</code></pre><ul><li>one</li></ul>`
		if got := docBody(template.HTML(in)); string(got.HTML) != in {
			t.Errorf("html = %s", got.HTML)
		}
	})
}
