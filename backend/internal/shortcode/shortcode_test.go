package shortcode

import (
	"reflect"
	"strings"
	"testing"
)

func echo(attrs map[string]string) (string, bool, bool) {
	return "<form data-s=\"" + attrs["slug"] + "\"></form>", true, true
}

func TestFindQuotesAndNonMatches(t *testing.T) {
	for in, want := range map[string][]string{
		`[form slug="a"]`:               {"a"},
		`[form slug='a']`:               {"a"},
		`[form slug=&#34;a&#34;]`:       {"a"},
		`[form slug=&quot;a-b_1&quot;]`: {"a-b_1"},
		`[form   slug = "a" ]`:          {"a"},
		`[format slug="a"]`:             nil,
		`[form-x slug="a"]`:             nil,
		`[Form slug="a"]`:               nil,
		`[form slug="A b"]`:             nil,
		`[form slug="a"] and [form slug="b"] [form slug="a"]`: {"a", "b"},
	} {
		if got := Slugs(in, "form"); !reflect.DeepEqual(got, want) {
			t.Errorf("Slugs(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestExpandLoneParagraphReplaced(t *testing.T) {
	reg := Registry{"form": echo}
	got := Expand(`<p>Hi</p><p>[form slug=&#34;contact&#34;]</p><p>Bye</p>`, reg)
	want := `<p>Hi</p><form data-s="contact"></form><p>Bye</p>`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandInlineKeepsParagraph(t *testing.T) {
	got := Expand(`<p>Write: [form slug="a"] now</p>`, Registry{"form": echo})
	if got != `<p>Write: <form data-s="a"></form> now</p>` {
		t.Errorf("got %q", got)
	}
}

func TestExpandTwoCalls(t *testing.T) {
	got := Expand(`<p>[form slug="a"]</p><p>x</p><p>[form slug="b"]</p>`, Registry{"form": echo})
	if strings.Count(got, "<form") != 2 || strings.Contains(got, "<p>[") || !strings.Contains(got, "<p>x</p>") {
		t.Errorf("got %q", got)
	}
}

func TestExpandNotOKRemoved(t *testing.T) {
	none := Registry{"form": func(map[string]string) (string, bool, bool) { return "", false, false }}
	if got := Expand(`<p>a</p><p>[form slug="x"]</p><p>b</p>`, none); got != `<p>a</p><p>b</p>` {
		t.Errorf("lone: %q", got)
	}
	if got := Expand(`<p>a [form slug="x"] b</p>`, none); got != `<p>a  b</p>` {
		t.Errorf("inline: %q", got)
	}
}

func TestExpandUnknownNameUntouched(t *testing.T) {
	in := `<p>[other slug="x"] [form slug="x"]</p>`
	got := Expand(in, Registry{"form": echo})
	if !strings.Contains(got, `[other slug="x"]`) || !strings.Contains(got, "<form") {
		t.Errorf("got %q", got)
	}
}

func TestRegistrySecondName(t *testing.T) {
	reg := Registry{"form": echo, "year": func(map[string]string) (string, bool, bool) { return "2026", false, true }}
	got := Expand(`<p>(c) [year]</p>`, reg)
	if got != `<p>(c) 2026</p>` {
		t.Errorf("got %q", got)
	}
}

func TestExpandSkipsCodeTagsAndAttributes(t *testing.T) {
	reg := Registry{"form": echo, "year": func(map[string]string) (string, bool, bool) { return "2026", false, true }}
	for _, in := range []string{
		`<p>Use <code>[form slug="contact"]</code> here</p>`,
		`<pre>[form slug="contact"]</pre>`,
		`<pre><code>[form slug=&#34;contact&#34;]</code></pre>`,
		`<p><a href="/x" title="[form slug=&#34;x&#34;]">l</a></p>`,
		`<p><img src="a.png" alt="[form slug=&#34;x&#34;]"></p>`,
		`<script>var a="[form slug=\"x\"]"</script><textarea>[form slug="x"]</textarea>`,
	} {
		if got := Expand(in, reg); got != in {
			t.Errorf("Expand(%q) = %q, want unchanged", in, got)
		}
		if got := Slugs(in, "form"); len(got) != 0 {
			t.Errorf("Slugs(%q) = %v, want none", in, got)
		}
	}
}

func TestExpandTextAfterCodeStillExpands(t *testing.T) {
	reg := Registry{"form": echo, "year": func(map[string]string) (string, bool, bool) { return "2026", false, true }}
	got := Expand(`<p><code>[year]</code> [year]</p><p>[form slug=&#34;c&#34;]</p>`, reg)
	want := `<p><code>[year]</code> 2026</p><form data-s="c"></form>`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
