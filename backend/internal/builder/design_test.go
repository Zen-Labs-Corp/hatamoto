package builder

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

var hostile = []string{"red;}", "url(x)", "</style>", "expression(1)", "1px;}body{x:y", "#fff;", "var(--x)", "", " 1rem"}

func TestTokenValueValidation(t *testing.T) {
	good := map[string]map[string]string{
		"color":  {"surface": "#fff", "muted": "#eef0f4", "accent": "#c8102e80"},
		"space":  {"0": "0", "4": "1.25rem", "12": "7rem"},
		"fs":     {"xs": "0.7rem", "4xl": "3.5rem"},
		"shadow": {"sm": "0 1px 2px rgba(0,0,0,.08)", "md": "0 4px 12px #00000033", "lg": "0 1px 2px #000, 0 8px 16px 2px rgba(0,0,0,0.2)"},
	}
	for class, m := range good {
		for name, v := range m {
			d := Design{Tokens: tokensOf(class, name, v)}
			if err := d.Validate(); err != nil {
				t.Errorf("%s.%s=%q rejected: %v", class, name, v, err)
			}
		}
	}
	for _, c := range tokenClasses {
		for _, v := range hostile {
			if c.valid(v) {
				t.Errorf("%s accepts %q", c.name, v)
			}
		}
	}
	for _, v := range []string{"0 1px 2px red", "0 1px rgba(0,0,0,.1)", "0 1px 2px 3px 4px #000", "0 1px 2px #000, 0 1px 2px #000, 0 1px 2px #000", "0 1px 2px rgba(0,0,0,.1);", "0 1px 2px url(x)"} {
		if shadowRe.MatchString(v) {
			t.Errorf("shadow accepts %q", v)
		}
	}
}

func tokensOf(class, name, v string) Tokens {
	var t Tokens
	for _, c := range tokenClasses {
		if c.name == class {
			*c.field(&t) = map[string]string{name: v}
		}
	}
	return t
}

func TestTokenNamesClosed(t *testing.T) {
	for class, name := range map[string]string{"color": "primary", "space": "13", "fs": "5xl", "shadow": "xl"} {
		d := Design{Tokens: tokensOf(class, name, map[string]string{"color": "#fff", "space": "1rem", "fs": "1rem", "shadow": "0 1px 2px #000"}[class])}
		if err := d.Validate(); err == nil {
			t.Errorf("%s token %q accepted", class, name)
		}
		if d.TokenCSS() != "" {
			t.Errorf("%s token %q emitted", class, name)
		}
	}
}

func TestTokenCSS(t *testing.T) {
	if (Design{}).TokenCSS() != "" {
		t.Fatal("empty design emitted a block")
	}
	if (Design{Tokens: DefaultTokens()}).TokenCSS() != "" {
		t.Fatal("default tokens emitted a block")
	}
	d := Design{Tokens: Tokens{
		Color:  map[string]string{"accent": "#112233", "surface": "#fff"},
		Space:  map[string]string{"4": "1.25rem", "2": "0.5rem"},
		FS:     map[string]string{"lg": "1.4rem"},
		Shadow: map[string]string{"md": "0 2px 4px rgba(0,0,0,.2)"},
	}}
	want := ":root{--hm-accent:#112233;--hm-fs-lg:1.4rem;--hm-shadow-md:0 2px 4px rgba(0,0,0,.2);--hm-space-4:1.25rem;}"
	for i := 0; i < 20; i++ {
		if got := d.TokenCSS(); got != want {
			t.Fatalf("got %s want %s", got, want)
		}
	}
}

func TestTokenCSSDropsInvalid(t *testing.T) {
	for _, v := range hostile {
		d := Design{Tokens: Tokens{Color: map[string]string{"accent": v}, Space: map[string]string{"1": v}, FS: map[string]string{"md": v}, Shadow: map[string]string{"sm": v}}}
		if got := d.TokenCSS(); got != "" {
			t.Errorf("hostile %q emitted: %s", v, got)
		}
	}
}

func card() Preset {
	return Preset{Label: "Card", S: map[string]map[string]string{
		"base": {"pt": "space-4", "bg": "surface", "radius": "md", "shadow": "sm"},
		"md":   {"pt": "space-3"},
		"sm":   {"pt": "space-2"},
	}}
}

func TestValidatePreset(t *testing.T) {
	if err := ValidatePreset("card", card()); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func() (string, Preset){
		"name":       func() (string, Preset) { return "Bad_Name", card() },
		"empty name": func() (string, Preset) { return "", card() },
		"no keys":    func() (string, Preset) { return "x", Preset{Label: "x"} },
		"empty maps": func() (string, Preset) { return "x", Preset{S: map[string]map[string]string{"base": {}}} },
		"label":      func() (string, Preset) { p := card(); p.Label = strings.Repeat("a", 61); return "x", p },
		"bp":         func() (string, Preset) { return "x", Preset{S: map[string]map[string]string{"lg": {"pt": "space-1"}}} },
		"key":        func() (string, Preset) { return "x", Preset{S: map[string]map[string]string{"base": {"zz": "1"}}} },
		"value":      func() (string, Preset) { return "x", Preset{S: map[string]map[string]string{"base": {"bg": "red;}"}}} },
	}
	for what, f := range bad {
		name, p := f()
		if err := ValidatePreset(name, p); err == nil {
			t.Errorf("%s accepted", what)
		}
	}
}

func TestValidateLimits(t *testing.T) {
	d := Design{Presets: map[string]Preset{}}
	for i := 0; i <= maxPresets; i++ {
		d.Presets["p"+strings.Repeat("a", i%20)+string(rune('a'+i/20))+string(rune('a'+i%20))] = card()
	}
	if len(d.Presets) != maxPresets+1 {
		t.Fatalf("setup: %d", len(d.Presets))
	}
	if err := d.Validate(); err == nil {
		t.Fatal("49 presets accepted")
	}
}

func TestParseDesignKeepsGoodDropsBad(t *testing.T) {
	blob := `{"colors":{"primary":"#000"},"builder":{"tokens":{"color":{"accent":"#abc","nope":"#fff","muted":"red;}"},"space":{"4":"2rem","5":7}},
	"presets":{"card":{"label":"Card","s":{"base":{"pt":"space-4","bg":"url(x)"}}},"Bad":{"s":{"base":{"pt":"space-1"}}},"empty":{"s":{"base":{"bg":"nope"}}}}}}`
	d, err := ParseDesign([]byte(blob))
	if err == nil {
		t.Fatal("expected a report of dropped entries")
	}
	if d.Tokens.Color["accent"] != "#abc" || d.Tokens.Space["4"] != "2rem" {
		t.Fatalf("good tokens lost: %+v", d.Tokens)
	}
	if _, ok := d.Tokens.Color["muted"]; ok || len(d.Tokens.Color) != 1 || len(d.Tokens.Space) != 1 {
		t.Fatalf("bad tokens kept: %+v", d.Tokens)
	}
	if len(d.Presets) != 1 || d.Presets["card"].S["base"]["pt"] != "space-4" || len(d.Presets["card"].S["base"]) != 1 {
		t.Fatalf("presets: %+v", d.Presets)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("parsed design must validate: %v", err)
	}
	for _, in := range []string{"", "null", `{}`, `{"builder":null}`, `not json`, `{"builder":"x"}`} {
		if d, _ := ParseDesign([]byte(in)); len(d.Presets) != 0 || !d.Tokens.empty() {
			t.Errorf("%q: non-empty design", in)
		}
	}
}

func TestPresetCSS(t *testing.T) {
	want := ".hm-p-card{background-color:var(--hm-surface,#fff);padding-top:var(--hm-space-4,1rem);border-radius:var(--hm-radius-md);box-shadow:var(--hm-shadow-sm,0 1px 2px rgba(0,0,0,.08));}" +
		"@media (max-width:1024px){.hm-p-card{padding-top:var(--hm-space-3,0.75rem);}}" +
		"@media (max-width:640px){.hm-p-card{padding-top:var(--hm-space-2,0.5rem);}}"
	if got := PresetCSS("card", card(), CSSOpts{}); got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if PresetCSS("Bad_Name", card(), CSSOpts{}) != "" {
		t.Fatal("invalid name emitted")
	}
	p := Preset{S: map[string]map[string]string{"base": {"fs": "lg", "bg": "red;}"}}}
	if got := PresetCSS("x", p, CSSOpts{Vars: true}); got != ".hm-p-x{font-size:var(--hm-fs-lg,1.25rem);}" {
		t.Fatalf("got %s", got)
	}
}

func TestMergeIntoKeepsOtherKeys(t *testing.T) {
	in := `{"colors":{"primary":"#000"},"socialLinks":[1,2],"builder":{"future":{"a":1},"tokens":{"space":{"1":"9rem"}}}}`
	d := Design{Tokens: Tokens{Space: map[string]string{"4": "2rem"}}, Presets: map[string]Preset{"card": card()}}
	out, err := d.MergeInto([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatal(err)
	}
	if string(top["colors"]) != `{"primary":"#000"}` || string(top["socialLinks"]) != `[1,2]` {
		t.Fatalf("other keys lost: %s", out)
	}
	back, err := ParseDesign(out)
	if err != nil || back.Tokens.Space["4"] != "2rem" || back.Tokens.Space["1"] != "" || back.Presets["card"].Label != "Card" {
		t.Fatalf("round trip: %+v %v", back, err)
	}
	if !strings.Contains(string(top["builder"]), `"future":{"a":1}`) {
		t.Fatalf("unknown builder subkey lost: %s", top["builder"])
	}
	// empty design removes the key, from nil or null input too
	for _, in := range []string{"", "null", "{}"} {
		out, err := (Design{}).MergeInto([]byte(in))
		if err != nil || string(out) != "{}" {
			t.Errorf("%q: %s %v", in, out, err)
		}
	}
	if _, err := d.MergeInto([]byte("not json")); err == nil {
		t.Fatal("bad input accepted")
	}
}

func TestDefaultTokensAreValid(t *testing.T) {
	d := Design{Tokens: DefaultTokens()}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(d.Tokens.Space) != 13 || len(d.Tokens.FS) != 8 || len(d.Tokens.Color) != 3 || len(d.Tokens.Shadow) != 3 {
		t.Fatalf("%+v", d.Tokens)
	}
}

func TestMergeTokensKeepsPresetsAsStored(t *testing.T) {
	in := `{"colors":{"a":1},"builder":{"presets":{"Bad Name":{"x":1}},"tokens":{"space":{"1":"9rem"}}}}`
	out, err := MergeTokens([]byte(in), Tokens{FS: map[string]string{"lg": "1.4rem"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"Bad Name":{"x":1}`) || !strings.Contains(string(out), `"colors":{"a":1}`) || strings.Contains(string(out), "9rem") || !strings.Contains(string(out), `"fs":{"lg":"1.4rem"}`) {
		t.Fatalf("got %s", out)
	}
	out, err = MergeTokens([]byte(`{"builder":{"tokens":{"space":{"1":"2rem"}}}}`), Tokens{})
	if err != nil || string(out) != "{}" {
		t.Fatalf("clearing tokens: %s %v", out, err)
	}
}

func TestValidateToken(t *testing.T) {
	for _, c := range []struct {
		class, name, v string
		ok             bool
	}{
		{"color", "accent", "#112233", true}, {"color", "accent", "red", false}, {"color", "primary", "#fff", false},
		{"space", "12", "7rem", true}, {"space", "13", "1rem", false}, {"fs", "2xl", "2rem", true},
		{"shadow", "lg", "0 1px 2px #000", true}, {"shadow", "lg", "none", false}, {"zzz", "a", "b", false},
	} {
		if err := ValidateToken(c.class, c.name, c.v); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestAddPreset(t *testing.T) {
	in := `{"colors":{"a":1},"builder":{"tokens":{"space":{"4":"2rem"}},"presets":{"old":{"label":"Old","s":{"base":{"pt":"space-1"}}},"Broken Name":{"x":1}}}}`
	out, err := AddPreset([]byte(in), "card", card())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"Broken Name":{"x":1}`) || !strings.Contains(string(out), `"colors":{"a":1}`) || !strings.Contains(string(out), "2rem") {
		t.Fatalf("other data lost: %s", out)
	}
	d, _ := ParseDesign(out)
	if d.Presets["card"].Label != "Card" || d.Presets["old"].Label != "Old" {
		t.Fatalf("presets: %+v", d.Presets)
	}
	if _, err := AddPreset(out, "card", card()); !errors.Is(err, ErrPresetExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := AddPreset([]byte(in), "Broken Name", card()); err == nil {
		t.Fatal("bad name accepted")
	}
	if _, err := AddPreset([]byte(in), "x", Preset{Label: "x"}); err == nil {
		t.Fatal("empty preset accepted")
	}
	// the 49th is refused
	cur := []byte(`{}`)
	for i := 0; i < maxPresets; i++ {
		if cur, err = AddPreset(cur, "p"+strings.Repeat("a", i/10)+string(rune('a'+i%10)), card()); err != nil {
			t.Fatalf("preset %d: %v", i, err)
		}
	}
	if _, err := AddPreset(cur, "overflow", card()); !errors.Is(err, ErrTooManyPresets) {
		t.Fatalf("49th: %v", err)
	}
}

func TestMergePresetsUpdateDeleteKeepsRest(t *testing.T) {
	in := `{"builder":{"presets":{"a":{"label":"A","s":{"base":{"pt":"space-1"}}},"b":{"label":"B","s":{"base":{"pt":"space-2"}}},"Raw Keep":{"x":1}}}}`
	p := card()
	out, err := MergePresets([]byte(in), map[string]*Preset{"a": &p, "b": nil})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := ParseDesign(out)
	if d.Presets["a"].Label != "Card" || len(d.Presets) != 1 || !strings.Contains(string(out), `"Raw Keep":{"x":1}`) {
		t.Fatalf("got %s", out)
	}
	bad := Preset{S: map[string]map[string]string{"base": {"bg": "red;}"}}}
	if _, err := MergePresets([]byte(in), map[string]*Preset{"a": &bad}); err == nil {
		t.Fatal("invalid preset merged")
	}
	if out, err := MergePresets([]byte(`{"builder":{"presets":{"a":{"label":"A","s":{"base":{"pt":"space-1"}}}}}}`), map[string]*Preset{"a": nil}); err != nil || string(out) != "{}" {
		t.Fatalf("deleting the last preset: %s %v", out, err)
	}
}

func TestValidateStyleValue(t *testing.T) {
	if ValidateStyleValue("pt", "space-4") != nil || ValidateStyleValue("pt", "red") == nil || ValidateStyleValue("nope", "1") == nil {
		t.Fatal("ValidateStyleValue")
	}
}

func TestParseDesignParts(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	in := `{"builder":{"parts":{"header":"` + id + `","footer":"nope","bogus":"` + id + `"}}}`
	d, err := ParseDesign([]byte(in))
	if err == nil || len(d.Parts) != 1 || d.Parts["header"] != id {
		t.Fatalf("parts = %v err = %v", d.Parts, err)
	}
}

func TestMergeParts(t *testing.T) {
	const a, b = "11111111-2222-3333-4444-555555555555", "aaaaaaaa-2222-3333-4444-555555555555"
	in := `{"colors":{"a":1},"builder":{"tokens":{"space":{"1":"2rem"}},"presets":{"x":{"y":1}}}}`
	out, err := MergeParts([]byte(in), map[string]string{"header": a, "footer": b})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{`"colors":{"a":1}`, `"tokens":{"space":{"1":"2rem"}}`, `"presets":{"x":{"y":1}}`, `"header":"` + a + `"`} {
		if !strings.Contains(string(out), w) {
			t.Fatalf("missing %s in %s", w, out)
		}
	}
	out, err = MergeParts(out, map[string]string{"header": ""})
	if err != nil || strings.Contains(string(out), `"header"`) || !strings.Contains(string(out), `"footer":"`+b+`"`) {
		t.Fatalf("remove one: %s %v", out, err)
	}
	out, err = MergeParts(out, map[string]string{"footer": ""})
	if err != nil || strings.Contains(string(out), "parts") {
		t.Fatalf("remove last: %s %v", out, err)
	}
	if _, err := MergeParts(nil, map[string]string{"page": a}); err == nil {
		t.Error("unknown slot accepted")
	}
	if _, err := MergeParts(nil, map[string]string{"header": "x"}); err == nil {
		t.Error("bad id accepted")
	}
	if out, err := MergeParts(nil, nil); err != nil || string(out) != "{}" {
		t.Errorf("empty: %s %v", out, err)
	}
}
