package a11y

import (
	"strings"
	"testing"
)

func TestCheckPublicWidgets(t *testing.T) {
	cases := []struct {
		name, html, err, warn string
	}{
		{"clean", `<img src="a" alt="x"><img src="b" alt=""><a href="/"> Go </a><button>Ok</button>`, "", ""},
		{"img no alt", `<img src="a">`, "no alt attribute", ""},
		{"link unnamed", `<a href="/"></a>`, "no accessible text", ""},
		{"button unnamed", `<button></button>`, "no accessible text", ""},
		{"table no th", `<table><tr><td>a</td></tr></table>`, "", "without a th"},
		{"table th", `<table><thead><tr><th scope="col">a</th></tr></thead></table>`, "", ""},
		{"details no summary", `<details><p>x</p></details>`, "details without a summary", ""},
		{"details ok", `<details><summary>T</summary><p>x</p></details>`, "", ""},
		{"inline style", `<p style="color:red">x</p>`, "inline style", ""},
		{"html widget skipped", `<div class="hm-n hm-html hm-w-html"><p style="x"></p><img src="x"></div>`, "", ""},
		{"tabs ok", `<div role="tablist"><button role="tab" id="a" aria-selected="true" aria-controls="p1">A</button><button role="tab" id="b" aria-selected="false" aria-controls="p2">B</button></div><section role="tabpanel" id="p1"></section><section role="tabpanel" id="p2"></section>`, "", ""},
		{"tabs no selected attr", `<div role="tablist"><button role="tab" aria-controls="p1">A</button></div><section role="tabpanel" id="p1"></section>`, "without aria-selected", ""},
		{"tabs bad panel", `<div role="tablist"><button role="tab" aria-selected="true" aria-controls="p1">A</button></div><div id="p1"></div>`, "role=tabpanel", ""},
		{"tabs missing id", `<div role="tablist"><button role="tab" aria-selected="true" aria-controls="zz">A</button></div>`, "missing id", ""},
		{"tabs two selected", `<div role="tablist"><button role="tab" aria-selected="true" aria-controls="p1">A</button><button role="tab" aria-selected="true" aria-controls="p1">B</button></div><div role="tabpanel" id="p1"></div>`, "2 selected", ""},
		{"blank no rel", `<a href="/x" target="_blank">Go</a>`, "without rel=noopener", ""},
		{"blank rel nofollow only", `<a href="/x" target="_blank" rel="nofollow">Go</a>`, "without rel=noopener", ""},
		{"blank ok", `<a href="/x" target="_blank" rel="noopener noreferrer">Go</a>`, "", ""},
		{"nested links", `<a href="/x">a <a href="/y">b</a></a>`, "nested inside another link", ""},
		{"sibling links", `<a href="/x">a</a><a href="/y">b</a>`, "", ""},
		{"nested link in html widget", `<div class="hm-w-html"><a href="/x">a <a href="/y">b</a></a></div>`, "", ""},
		{"lightbox empty alt", `<a href="/x" data-hm-lightbox><img src="a" alt=""></a>`, "no accessible text", ""},
		{"lightbox empty alt named", `<a href="/x" data-hm-lightbox aria-label="Big"><img src="a" alt=""></a>`, "", ""},
		{"lightbox alt", `<a href="/x" data-hm-lightbox><img src="a" alt="Cat"></a>`, "", ""},
		{"heading skip", `<h1>a</h1><h3>b</h3>`, "", "jumps from h1 to h3"},
		{"heading ok", `<h2>a</h2><h3>b</h3><h2>c</h2>`, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs, warns := CheckPublicWidgets(c.html)
			if got := strings.Join(errs, "; "); (c.err == "") != (got == "") || !strings.Contains(got, c.err) {
				t.Errorf("errs = %q, want containing %q", got, c.err)
			}
			if got := strings.Join(warns, "; "); (c.warn == "") != (got == "") || !strings.Contains(got, c.warn) {
				t.Errorf("warns = %q, want containing %q", got, c.warn)
			}
		})
	}
}
