package schemafield

import "testing"

func TestMentionsImageMarkup(t *testing.T) {
	for in, want := range map[string]bool{
		`<img src="https://cdn.example/k.jpg">`:  true,
		`<IMG SRC=x>`:                            true,
		`<img src="/%6Dedia/k.jpg">`:             true,
		`<a href=x><img srcset="a 1x"></a>`:      true,
		`<div srcset="a 1x">x</div>`:             true,
		`<picture><source srcset="a"></picture>`: true,
		`<video poster=x></video>`:               true,
		`<div style="background:url(x)">`:        true,
		`<div style="BACKGROUND: URL( x )">`:     true,
		`<style>p{background:url(x)}</style>`:    true,
		`<table background="x">`:                 true,
		`<svg><image href="x"/></svg>`:           true,
		`<svg><use xlink:href="#a"/></svg>`:      true,
		`<object data=x></object>`:               true,
		`<embed src=x>`:                          true,
		`<iframe src=x></iframe>`:                true,
		`<audio src=x></audio>`:                  true,
		`![a](x)`:                                true,
		`&lt;img src=x&gt;`:                      true,
		`&amp;lt;img src=x&amp;gt;`:              true,
		`%3Cimg src=x%3E`:                        true,
		`<img src="data:image/png;base64,AAAA">`: true,
		`see data:text/html,hi`:                  true,
		`<p>Hello <a href="https://example.com/a">a</a></p><h2>Title</h2>`: false,
		`<p>Images are great! Use the metadata: field.</p>`:                false,
		`<div style="color:red">x</div>`:                                   false,
		"":                                                                 false, "plain text": false, `a < b and c > d`: false,
	} {
		if got := MentionsImageMarkup(in); got != want {
			t.Errorf("MentionsImageMarkup(%q) = %v, want %v", in, got, want)
		}
	}
}
