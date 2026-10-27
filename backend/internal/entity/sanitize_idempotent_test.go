package entity

import "maps"

import "testing"

// The html section sanitiser must be a fixed point: stored content is
// normalised to canonical form, and re-saving or re-running the migration
// must not change it again.
func TestHTMLSectionPolicy_Idempotent(t *testing.T) {
	inputs := map[string]string{}
	for _, s := range loadRichSamples(t) {
		inputs[s.Name] = s.HTML
	}
	edge := map[string]string{
		"style-url":      `<p style="background:url('/a.png');color:red">x</p><div style="background:url( https://e.com/a.png )">y</div>`,
		"style-rejected": `<p style="background:url(javascript:1)">x</p><p style="width:expression(1)">y</p>`,
		"svg":            `<svg viewBox="0 0 10 10" xmlns="http://www.w3.org/2000/svg"><g><path d="M0 0L5 5" fill="none"/><circle cx="1" cy="1" r="2"/></g></svg>`,
		"details":        `<details open><summary>Q</summary><p>A</p></details>`,
		"entities":       `<p>a &amp; b &nbsp; &lt;c&gt; &copy; &quot;q&quot; &amp;amp; &#39;x&#39;</p>`,
		"attr-entities":  `<a href="/x?a=1&amp;b=2" title="a &amp; &quot;b&quot;">l</a>`,
		"self-closing":   `<p>a<br/>b<hr/><img src="/a.png" alt="x"/><div/>c</p>`,
		"unclosed":       `<div><p>one<p>two<b>bold`,
		"mixed-unsafe":   `<script>x</script><p onclick="y">z</p><iframe src="https://evil.com/"></iframe>`,
		"plain":          `just text & more`,
		"empty":          ``,
	}
	maps.Copy(inputs, edge)
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			once := htmlSection().Sanitize(in)
			twice := htmlSection().Sanitize(once)
			if once != twice {
				t.Errorf("not idempotent\n in:    %q\n once:  %q\n twice: %q", in, once, twice)
			}
		})
	}
}
