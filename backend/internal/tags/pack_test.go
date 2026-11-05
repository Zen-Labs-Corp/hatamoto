package tags

import (
	"strings"
	"sync"
	"testing"
)

const goodDecl = `{
 "name": "Acme Stats",
 "id": {"label": "Site ID", "pattern": "^[0-9]{6,9}$"},
 "script": "https://static.acme.example/c/acme-{id}.js?sv=6",
 "hosts": ["static.acme.example", "*.collect.acme.example"],
 "websockets": true,
 "category": "analytics",
 "noscript": "https://x.collect.acme.example/px?id={id}&v=1"
}`

func TestParseDeclValid(t *testing.T) {
	v, err := ParseDecl("acme", "stats", []byte(goodDecl))
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != "acme.stats" || v.Boot != "generic" || v.Category != "analytics" || v.Label != "Acme Stats" {
		t.Fatalf("vendor = %+v", v)
	}
	if got := Expand(v.Src, "123456"); got != "https://static.acme.example/c/acme-123456.js?sv=6" {
		t.Fatalf("src = %q", got)
	}
	wantScript := []string{"https://static.acme.example", "https://*.collect.acme.example"}
	if strings.Join(v.Script, " ") != strings.Join(wantScript, " ") {
		t.Fatalf("script hosts = %v", v.Script)
	}
	wantConnect := append(append([]string{}, wantScript...), "wss://static.acme.example", "wss://*.collect.acme.example")
	if strings.Join(v.Connect, " ") != strings.Join(wantConnect, " ") {
		t.Fatalf("connect hosts = %v", v.Connect)
	}
	if !strings.Contains(v.Noscript, `src="https://x.collect.acme.example/px?id={id}&amp;v=1"`) || !strings.HasPrefix(v.Noscript, "<img ") {
		t.Fatalf("noscript = %q", v.Noscript)
	}
	if !v.ValidID("123456") || v.ValidID("12") || v.ValidID("123456\n") {
		t.Fatal("pattern not applied whole-value")
	}
}

func TestParseDeclNoWebsocketsNoNoscript(t *testing.T) {
	raw := `{"name":"A","id":{"label":"ID","pattern":"^[a-z]+$"},"script":"https://a.example/x.js","hosts":["a.example"],"category":"functional"}`
	v, err := ParseDecl("pk", "a", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Connect) != 1 || v.Noscript != "" {
		t.Fatalf("vendor = %+v", v)
	}
}

func TestParseDeclRefusals(t *testing.T) {
	mod := func(old, repl string) string { return strings.Replace(goodDecl, old, repl, 1) }
	cases := []struct{ name, raw, want string }{
		{"unknown key", mod(`"websockets": true,`, `"websockets": true, "extra": 1,`), "unknown object member"},
		{"trailing data", goodDecl + " {}", "after top-level value"},
		{"not json", `nope`, "invalid JSON"},
		{"empty name", mod(`"Acme Stats"`, `""`), "name is required"},
		{"long name", mod(`"Acme Stats"`, `"`+strings.Repeat("x", 61)+`"`), "name is longer"},
		{"markup in name", mod(`"Acme Stats"`, `"<b>x"`), "not allowed"},
		{"empty label", mod(`"Site ID"`, `""`), "id.label is required"},
		{"unanchored start", mod(`"^[0-9]{6,9}$"`, `"[0-9]{6,9}$"`), "start with ^"},
		{"unanchored end", mod(`"^[0-9]{6,9}$"`, `"^[0-9]{6,9}"`), "end with $"},
		{"escaped dollar", mod(`"^[0-9]{6,9}$"`, `"^[0-9]{6,9}\\$"`), "end with $"},
		{"empty match", mod(`"^[0-9]{6,9}$"`, `"^[0-9]*$"`), "empty id"},
		{"bad regexp", mod(`"^[0-9]{6,9}$"`, `"^(a$"`), "not a valid expression"},
		{"lookahead", mod(`"^[0-9]{6,9}$"`, `"^(?=a)a$"`), "not a valid expression"},
		{"long pattern", mod(`"^[0-9]{6,9}$"`, `"^`+strings.Repeat("a", 120)+`$"`), "longer than 120"},
		{"necessary", mod(`"analytics"`, `"necessary"`), "category must be"},
		{"no category", mod(`"category": "analytics",`, ``), "category must be"},
		{"http script", mod(`"https://static.acme.example/c`, `"http://static.acme.example/c`), "script must be an https"},
		{"foreign script host", mod(`https://static.acme.example/c`, `https://evil.example/c`), `script host "evil.example" is not in hosts`},
		{"wildcard apex", mod(`https://static.acme.example/c`, `https://collect.acme.example/c`), "is not in hosts"},
		{"script port", mod(`https://static.acme.example/c`, `https://static.acme.example:8443/c`), "port"},
		{"script credentials", mod(`https://static.acme.example/c`, `https://u:p@static.acme.example/c`), "credentials"},
		{"id twice", mod(`acme-{id}.js`, `{id}-{id}.js`), "only once"},
		{"id in host", mod(`https://static.acme.example/c`, `https://{id}.acme.example/c`), "script"},
		{"no script", mod(`"script": "https://static.acme.example/c/acme-{id}.js?sv=6",`, ``), "script is required"},
		{"http noscript", mod(`"https://x.collect.acme.example/px`, `"http://x.collect.acme.example/px`), "noscript must be an https"},
		{"foreign noscript", mod(`"https://x.collect.acme.example/px`, `"https://evil.example/px`), "noscript host"},
		{"no hosts", mod(`["static.acme.example", "*.collect.acme.example"]`, `[]`), "at least one host"},
		{"bare tld wildcard", mod(`*.collect.acme.example`, `*.com`), "must be a lower-case hostname"},
		{"single label host", mod(`"static.acme.example"`, `"localhost"`), "must be a lower-case hostname"},
		{"upper host", mod(`"static.acme.example"`, `"Static.acme.example"`), "must be a lower-case hostname"},
		{"host with path", mod(`"static.acme.example"`, `"static.acme.example/x"`), "must be a lower-case hostname"},
		{"duplicate host", mod(`"*.collect.acme.example"`, `"static.acme.example"`), "listed twice"},
		{"nine hosts", mod(`["static.acme.example", "*.collect.acme.example"]`, `["a1.example","a2.example","a3.example","a4.example","a5.example","a6.example","a7.example","a8.example","a9.example"]`), "more than 8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDecl("acme", "stats", []byte(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := ParseDecl("acme", "Bad Stem", []byte(goodDecl)); err == nil {
		t.Fatal("bad stem accepted")
	}
	if _, err := ParseDecl("Bad Plugin", "stats", []byte(goodDecl)); err == nil {
		t.Fatal("bad plugin accepted")
	}
}

func TestPatternAlternationCannotEscapeAnchors(t *testing.T) {
	raw := strings.Replace(goodDecl, `"^[0-9]{6,9}$"`, `"^[0-9]{6}$|[a-z]$"`, 1)
	v, err := ParseDecl("acme", "stats", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if v.ValidID("xx-abc-xx") || !v.ValidID("123456") || v.ValidID("abc") {
		t.Fatal("alternation escaped the anchors")
	}
}

func TestAllAndByIDWithPacks(t *testing.T) {
	t.Cleanup(func() { SetPacks(nil) })
	v, err := ParseDecl("acme", "stats", []byte(goodDecl))
	if err != nil {
		t.Fatal(err)
	}
	base := len(Vendors)
	if len(All()) != base {
		t.Fatalf("All without packs = %d", len(All()))
	}
	SetPacks([]PackVendor{{Vendor: v, PluginVersion: "1.0.0"}})
	all := All()
	if len(all) != base+1 || all[base].ID != "acme.stats" || all[0].ID != Vendors[0].ID {
		t.Fatalf("All = %d vendors, last %q", len(all), all[len(all)-1].ID)
	}
	all[0].Label = "mutated"
	if Vendors[0].Label == "mutated" || All()[0].Label == "mutated" {
		t.Fatal("All shares storage")
	}
	if got, ok := ByID("acme.stats"); !ok || got.Label != "Acme Stats" {
		t.Fatal("ByID misses pack vendor")
	}
	if p := Packs(); len(p) != 1 || p[0].PluginVersion != "1.0.0" {
		t.Fatalf("Packs = %+v", p)
	}
	SetPacks(nil)
	if _, ok := ByID("acme.stats"); ok {
		t.Fatal("pack vendor survived SetPacks(nil)")
	}
}

func TestSetPacksRace(t *testing.T) {
	t.Cleanup(func() { SetPacks(nil) })
	v, _ := ParseDecl("acme", "stats", []byte(goodDecl))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				SetPacks([]PackVendor{{Vendor: v}})
				SetPacks(nil)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if n := len(All()); n != len(Vendors) && n != len(Vendors)+1 {
					t.Errorf("torn list of %d", n)
				}
				ByID("acme.stats")
			}
		}()
	}
	wg.Wait()
}

func TestIsPackKey(t *testing.T) {
	if !IsPackKey("acme.stats") || IsPackKey("ga4") {
		t.Fatal("IsPackKey")
	}
	for _, v := range Vendors {
		if IsPackKey(v.ID) {
			t.Fatalf("built-in %q looks like a pack key", v.ID)
		}
	}
}

func TestParseDeclKeysCaseInsensitive(t *testing.T) {
	raw := strings.NewReplacer(`"name"`, `"Name"`, `"script"`, `"Script"`).Replace(goodDecl)
	if _, err := ParseDecl("acme", "stats", []byte(raw)); err != nil {
		t.Fatal(err)
	}
}
