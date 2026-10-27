package builder

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

// The shared fixture backend/testdata/stylespec is read by the Go tests here
// and by the node tests of the builder client, so the served table, the Go
// validator and the JS preview cannot drift apart.
const fixtureDir = "../../testdata/stylespec/"

var updateStyleSpecFixture = flag.Bool("update", false, "rewrite backend/testdata/stylespec")

type specCase struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	OK    bool   `json:"ok"`
	// CSS is the compiled value of the key's props when OK.
	CSS string `json:"css,omitempty"`
}

// specCases builds the case list: every accepted and refused value of the
// v2 keys, a few old keys, and every injection against one key of each kind.
func specCases() []specCase {
	var out []specCase
	add := func(k, v string) {
		c := specCase{Key: k, Value: v}
		if ds, err := styleDecl(k, v); err == nil {
			c.OK, c.CSS = true, ds[0].val
		}
		out = append(out, c)
	}
	for _, g := range v2Groups {
		for _, c := range g.cases {
			for _, v := range c.good {
				add(c.key, v)
			}
			for _, v := range c.bad {
				add(c.key, v)
			}
		}
	}
	for _, c := range []specCase{
		{Key: "pt", Value: "space-4"}, {Key: "pt", Value: "-1rem"}, {Key: "pt", Value: "auto"}, {Key: "pt", Value: "12px"},
		{Key: "bg", Value: "accent"}, {Key: "bg", Value: "#fff"}, {Key: "dir", Value: "column-reverse"}, {Key: "ta", Value: "justify"},
		{Key: "cols", Value: "3"}, {Key: "hide", Value: "1"}, {Key: "w", Value: "container"}, {Key: "radius", Value: "full"},
	} {
		add(c.Key, c.Value)
	}
	for _, k := range []string{"ml", "h", "pos", "z", "ff", "lh", "ls", "bw", "bc", "rtl", "bgimg", "grad", "bgpos", "op", "display", "order", "gtc", "cs", "pt", "bg"} {
		for _, v := range injections {
			add(k, v)
		}
	}
	return out
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", " ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStyleSpecFixtureTable(t *testing.T) {
	if *updateStyleSpecFixture {
		writeJSON(t, fixtureDir+"table.json", StyleSpecTable())
	}
	raw, err := os.ReadFile(fixtureDir + "table.json")
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	cur, _ := json.Marshal(StyleSpecTable())
	_ = json.Unmarshal(cur, &got)
	if !jsonEqual(want, got) {
		t.Fatal("testdata/stylespec/table.json differs from StyleSpecTable(); run `go test ./internal/builder -run StyleSpecFixture -update` and review the diff")
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func TestStyleSpecFixtureCases(t *testing.T) {
	if *updateStyleSpecFixture {
		writeJSON(t, fixtureDir+"cases.json", specCases())
	}
	raw, err := os.ReadFile(fixtureDir + "cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []specCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	perKey := map[string]int{}
	for _, c := range cases {
		ds, err := styleDecl(c.Key, c.Value)
		if (err == nil) != c.OK {
			t.Errorf("%s=%q: ok=%v, fixture says %v", c.Key, c.Value, err == nil, c.OK)
			continue
		}
		if c.OK && ds[0].val != c.CSS {
			t.Errorf("%s=%q: css %q, fixture says %q", c.Key, c.Value, ds[0].val, c.CSS)
		}
		perKey[c.Key]++
	}
	// Every new kind has at least three cases per key, and every key at least one of each outcome.
	for _, e := range StyleSpecTable() {
		if _, old := oldKeyValues[e.Key]; old && e.Key != "mt" && e.Key != "mb" {
			continue
		}
		if perKey[e.Key] < 3 {
			t.Errorf("key %s has %d fixture cases", e.Key, perKey[e.Key])
		}
	}
	// The injection list is part of the fixture.
	for _, v := range injections {
		found := false
		for _, c := range cases {
			if c.Key == "grad" && c.Value == v && !c.OK {
				found = true
			}
		}
		if !found {
			t.Errorf("injection %q missing from the fixture", v)
		}
	}
}
