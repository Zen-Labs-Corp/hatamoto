package render

import (
	"strings"
	"testing"
)

// Where an action may sit in a plugin section or partial without site.scripts:
// a printing action in text or entirely inside a quoted attribute value;
// control structures and template calls only in text, every branch starting and
// ending in text. Nothing dynamic anywhere else in a tag.

var pluginActionsAccepted = map[string]string{
	"text":                   `<p>{{.Content.t}}</p>`,
	"text in if":             `{{if .Content.x}}<p>{{.Content.t}}</p>{{end}}`,
	"quoted value":           `<p class="a {{.Content.c}}" title='{{.Content.t}}'>x</p>`,
	"data attr":              `<p data-id="{{.ID}}">x</p>`,
	"href safe":              `<a href="/p/{{.Content.slug}}">go</a>`,
	"href whole":             `<a href="{{.Content.u}}">go</a>`,
	"printing func in value": `<input checked="{{.Content.on | printf "%v"}}">`,
	"print in value":         `<p title='{{print .Content.t "x"}}'>x</p>`,
	"template call":          `<div>{{template "section-other" .}}</div>`,
	"if around elements":     `{{if .Content.on}}<input checked>{{else}}<input>{{end}}`,
	"if whole tags":          `{{if .Content.on}}<p class="a {{.Content.c}}">x</p>{{end}}`,
	"range text":             `<ul>{{range .Content.items}}<li>{{.}}</li>{{end}}</ul>`,
	"with text":              `{{with .Content.t}}<p>{{.}}</p>{{end}}`,
	"comment":                `<!-- a > b --><p>x {{.Content.t}}</p>`,
	"empty comments":         `<!----><!--><!---><p>x</p>`,
	"style without actions":  `<style>.a{color:red}</style><p>{{.Content.t}}</p>`,
	"range break":            `{{range .Content.xs}}{{if .}}<b>{{.}}</b>{{break}}{{end}}{{end}}`,
	"range continue":         `{{range .Content.xs}}{{if .}}{{continue}}{{end}}<b>{{.}}</b>{{end}}`,
	"end tag text":           `<p>a</p> {{.Content.t}} </p>`,
	"doctype":                `<!DOCTYPE html><p>x</p>`,
	"lt in text":             `<p>a < b {{.Content.t}}</p>`,
	"form exception":         `<form action="{{.Content.u}}" method="post"><input name="a" value="{{.Content.v}}"></form>`,
	"style exception":        `<style>.a{color:red}</style><p class="a" style="color:{{.Content.c}}">x</p>`,
}

var pluginActionsRefused = map[string]string{
	"element name":           `<scr{{"ipt"}}>x()</scr{{"ipt"}}>`,
	"element name data":      `<p><scr{{.Content.t}}>x()</scr{{.Content.t}}></p>`,
	"whole element name":     `<{{.Content.tag}}>x</{{.Content.tag}}>`,
	"end tag name":           `<p>x</{{.Content.tag}}>`,
	"attr name":              `<p {{.Content.a}}>x</p>`,
	"attr name part":         `<p o{{.Content.a}}="x()">x</p>`,
	"attr name split":        `<p {{"o"}}{{"nclick"}}="x()">x</p>`,
	"attr after value":       `<p class="a"{{.Content.a}}>x</p>`,
	"attr name before =":     `<p x{{.Content.a}}=1>x</p>`,
	"attr after name":        `<p x {{.Content.a}}>x</p>`,
	"name behind if":         `{{if .Content.x}}<p><scr{{"ipt"}}>x()</scr{{"ipt"}}></p>{{end}}`,
	"attr behind if":         `{{if .Content.x}}<p {{"o"}}{{"nclick"}}>x</p>{{end}}`,
	"name behind range":      `{{range .Content.items}}<scr{{"ipt"}}>x()</scr{{"ipt"}}>{{end}}`,
	"attr behind range":      `{{range .Content.items}}<p {{.}}>x</p>{{end}}`,
	"name behind with":       `{{with .Content.t}}<scr{{.}}>x()</scr{{.}}>{{end}}`,
	"attr behind with":       `{{with .Content.t}}<p {{.}}>x</p>{{end}}`,
	"name behind else":       `{{if .Content.x}}<b>y</b>{{else}}<scr{{.Content.t}}>x()</scr{{.Content.t}}>{{end}}`,
	"content as name":        `<{{.Content}}>x</p>`,
	"content as attr":        `<p {{.Content}}>x</p>`,
	"template in name":       `<scr{{template "section-other" .}}>x</p>`,
	"template as attr":       `<p {{template "section-other" .}}>x</p>`,
	"after comment":          `<!-- a > <scr{{"ipt"}}>x()</scr{{"ipt"}}> -->`,
	"after empty comment":    `<!--> <p {{.Content.a}}>x</p>`,
	"if name in tag":         `<p{{if .Content.x}} a{{end}}{{.Content.t}}>x</p>`,
	"action in tag in if":    `<p {{if .Content.x}}{{.Content.a}}{{end}}>x</p>`,
	"name continues value":   `<p class="a"><b{{.Content.t}}>x</b></p>`,
	"after unquoted value":   `<p class=a {{.Content.a}}>x</p>`,
	"template in value":      `<div class="{{template "section-other" .}}">x</div>`,
	"template in single q":   `<div class='{{template "section-other" .}}'>x</div>`,
	"template in unquoted":   `<div class={{template "section-other" .}}>x</div>`,
	"template in tag":        `<div {{template "section-other" .}}>x</div>`,
	"if between attrs":       `<input type="checkbox" {{if .Content.on}}checked{{end}}>`,
	"if then space":          `<input type="checkbox" {{if .Content.on}}checked{{end}} disabled>`,
	"if in value":            `<p class="a{{if .Content.x}} b{{end}}">x</p>`,
	"if whole value":         `<input checked="{{if .Content.on}}checked{{end}}">`,
	"else in value":          `<p class="{{if .Content.x}}a{{else}}b{{end}}">x</p>`,
	"with in value":          `<p class="{{with .Content.x}}{{.}}{{end}}">x</p>`,
	"range in value":         `<p class="{{range .Content.xs}}{{.}} {{end}}">x</p>`,
	"block in tag":           `<p {{block "b" .}}x{{end}}>x</p>`,
	"range joins iterations": `<input {{range .Content.xs}}c{{end}}>`,
	"range if joins":         `<input {{range .Content.xs}}{{if .}}a{{else}}b{{end}}{{end}}>`,
	"range checked":          `<input {{range .Content.xs}}checked {{end}}>`,
	"if glued name":          `<input {{if .Content.x}}chec{{else}}dis{{end}}ked>`,
	"if glued value":         `<input {{if .Content.x}}checked{{end}}=1>`,
	"if glued space value":   `<input {{if .Content.x}}checked{{end}} =1>`,
	"if space before =":      `<input {{if .Content.x}}checked {{else}}disabled{{end}} =1>`,
	"if starts in name":      `<input chec{{if .Content.x}}ked{{end}}>`,
	"if in tag after name":   `<input checked {{if .Content.x}}a{{end}}>`,
	"branch opens tag":       `{{if .Content.x}}<p class="{{else}}<p class="b">{{end}}x">`,
	"unquoted value":         `<p class={{.Content.c}}>x</p>`,
	"unquoted after eq":      `<p class= {{.Content.c}}>x</p>`,
	"name after text close":  `<p>a</p><{{.Content.t}} src=x>`,
	"unquoted started":       `<p class=a{{.Content.c}}>x</p>`,
	"unquoted started if":    `<p class=a{{if .Content.x}}b{{end}}>x</p>`,
	"unquoted started tmpl":  `<p class=a{{template "section-other" .}}>x</p>`,
	"break in tag":           `{{range .Content.xs}}<input {{break}}>{{end}}x`,
	"continue in tag":        `{{range .Content.xs}}<input {{continue}}>{{end}}x`,
	"break in value":         `{{range .Content.xs}}<p class="{{break}}">x</p>{{end}}`,
	"break in comment":       `{{range .Content.xs}}<!-- {{break}} -->{{end}}x`,
	"comment desync":         `<!-- > <b title=" --><input {{.Content.n}} title="x">`,
	"style desync":           `<style>/* <b title=" */</style><input {{.Content.n}} title="x">`,
	"style desync single":    `<style>/* <b title=' */</style><input {{.Content.n}} title="x">`,
	"print in comment":       `<!-- {{.Content.t}} --><p>x</p>`,
	"print in comment 2":     `<!-- a > {{.Content.t}} --><p>x</p>`,
	"if in comment":          `<!-- {{if .Content.x}}a{{end}} --><p>x</p>`,
	"template in comment":    `<!-- {{template "section-other" .}} --><p>x</p>`,
	"print in bang":          `<!x {{.Content.t}}><p>x</p>`,
	"print in pi":            `<?x {{.Content.t}}><p>x</p>`,
	"print in doctype":       `<!DOCTYPE {{.Content.t}}><p>x</p>`,
	"print in cdata":         `<![CDATA[ {{.Content.t}} ]]><p>x</p>`,
	"print in style":         `<style>.a{color:{{.Content.c}}}</style>`,
	"if in style":            `<style>{{if .Content.x}}.a{color:red}{{end}}</style>`,
	"template in style":      `<style>{{template "section-other" .}}</style>`,
	"print in textarea":      `<textarea>{{.Content.t}}</textarea>`,
	"print in title":         `<title>{{.Content.t}}</title>`,
	"print in xmp":           `<xmp>{{.Content.t}}</xmp>`,
	"print in iframe":        `<iframe>{{.Content.t}}</iframe>`,
	"print in noembed":       `<noembed>{{.Content.t}}</noembed>`,
	"print in noframes":      `<noframes>{{.Content.t}}</noframes>`,
	"print in noscript":      `<noscript>{{.Content.t}}</noscript>`,
	"print in plaintext":     `<plaintext>{{.Content.t}}`,
	"print in script":        `<script>{{.Content.t}}</script>`,
	"print in self-closing":  `<style/>{{.Content.t}}`,
	"print in style upper":   `<STYLE>{{.Content.t}}</STYLE>`,
	"style not closed":       `<style>.a{color:red}`,
	"comment not closed":     `<!-- a --`,
	"comment not closed 2":   `<!-- a >`,
	"branch opens style":     `{{if .Content.x}}<style>.a{}{{end}}</style>`,
	"branch opens comment":   `{{if .Content.x}}<!-- {{else}} --> {{end}}`,
	"print in end tag":       `<p>x</p {{.Content.t}}>`,
	"end tag name data":      `<p>x</{{.Content.t}}>`,
	"print in a name, quote": `<p title="a" {{.Content.t}} class="b">x</p>`,
	"quote in unquoted":      `<p class=a" {{.Content.t}} ">x</p>`,
	"dup attr name":          `<p a="1" a{{.Content.t}}="2">x</p>`,
}

func TestPluginMarkupActionsNeedTheGrant(t *testing.T) { //nolint:paralleltest // sets the global script gate
	for name, m := range pluginActionsAccepted {
		got := gatedSections(t, map[string]string{"s": m}, false)
		if !got["s"] {
			t.Errorf("accepted/%s: refused without site.scripts: %s", name, m)
		}
	}
	for name, m := range pluginActionsRefused {
		if gatedSections(t, map[string]string{"s": m}, false)["s"] {
			t.Errorf("refused/%s: registered without site.scripts: %s", name, m)
		}
		if !gatedSections(t, map[string]string{"s": m}, true)["s"] {
			t.Errorf("refused/%s: not registered with site.scripts: %s", name, m)
		}
	}
}

// The reason an admin is shown names the rule, not a generic scan failure.
func TestPluginMarkupActionsReason(t *testing.T) {
	err := checkPluginScripts(`{{define "s"}}<p {{.Content.a}}>x</p>{{end}}`, nil)
	if err == nil {
		t.Fatal("an action in an attribute name was accepted")
	}
	for _, want := range []string{"action-context", "without site.scripts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("reason %q lacks %q", err, want)
		}
	}
}

// A partial goes through the same check as a section.
func TestPluginMarkupActionsPartials(t *testing.T) { //nolint:paralleltest // sets the global script gate
	SetPluginScriptGate(func(string) bool { return false })
	t.Cleanup(func() { SetPluginScriptGate(nil) })
	reg := newPluginRegistry()
	if err := reg.registerPartial("p", "ok", `<p class="{{.Content.c}}">{{.Content.t}}</p>`); err != nil {
		t.Fatal(err)
	}
	if err := reg.registerPartial("p", "bad", `<scr{{.Content.t}}>x()</scr{{.Content.t}}>`); err != nil {
		t.Fatal(err)
	}
	snap := reg.gated()
	if snap.partials["plugin/p/ok"] == " " {
		t.Error("a partial with actions in a value and in text was blanked")
	}
	if snap.partials["plugin/p/bad"] != " " {
		t.Errorf("the partial with an action in an element name is registered: %q", snap.partials["plugin/p/bad"])
	}
}

// A template that ends inside a tag would leave its caller in that tag, so no
// defined template may (the pack rule), while one that ends in text may be
// called from text. A call inside a tag is refused by the table above.
func TestPluginMarkupActionsDefineEnd(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		src string
		ok  bool
	}{
		"ends in text":      {`{{define "plugin/p/a"}}<p>x</p>{{end}}<div>{{template "plugin/p/a" .}}</div>`, true},
		"ends in tag":       {`{{define "plugin/p/a"}}<p {{end}}`, false},
		"ends in tag, call": {`{{define "plugin/p/a"}}<p {{end}}<div>{{template "plugin/p/a" .}}x="y">z</div>`, false},
		"ends in attr name": {`{{define "plugin/p/a"}}<p onc{{end}}`, false},
		"ends in value":     {`{{define "plugin/p/a"}}<p class="a{{end}}`, false},
	}
	for name, c := range cases {
		err := checkPluginScripts(c.src, nil)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", name, err, c.ok)
		}
	}
}
