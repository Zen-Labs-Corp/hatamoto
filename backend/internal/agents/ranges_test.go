package agents

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"
)

func parseFixture(t *testing.T, name string) []netip.Prefix {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePrefixes(b)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return p
}

func prefixStrings(ps []netip.Prefix) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

func TestParsePrefixesShapes(t *testing.T) {
	cases := []struct {
		file string
		want []string
	}{
		{"google.json", []string{"2001:4860:4801:10::/64", "66.249.64.0/27", "192.0.2.7/32", "2001:db8::1/128"}},
		{"openai.json", []string{"20.171.206.0/24", "52.230.152.0/24"}},
		{"ahrefs-like.json", []string{"54.36.148.0/24", "198.51.100.9/32", "203.0.113.0/25"}},
		{"plain-array.json", []string{"10.1.2.0/24", "2001:db8:abcd::/48", "172.16.0.5/32"}},
	}
	for _, c := range cases {
		got := prefixStrings(parseFixture(t, c.file))
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %v, want %v", c.file, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s[%d]: got %s, want %s", c.file, i, got[i], c.want[i])
			}
		}
	}
}

func TestParsePrefixesMapsV4InV6(t *testing.T) {
	got, err := ParsePrefixes([]byte(`["::ffff:203.0.113.0/120", "::ffff:198.51.100.4"]`))
	if err != nil {
		t.Fatal(err)
	}
	s := prefixStrings(got)
	if s[0] != "203.0.113.0/24" || s[1] != "198.51.100.4/32" {
		t.Errorf("got %v", s)
	}
}

func TestParsePrefixesRejectsUnusable(t *testing.T) {
	for _, body := range []string{``, `garbage`, `<html></html>`, `{}`, `[]`, `{"prefixes":[]}`, `{"prefixes":[{"ipv4Prefix":"x"}]}`, `{"other":1}`, `42`} {
		if p, err := ParsePrefixes([]byte(body)); err == nil {
			t.Errorf("%q: want error, got %v", body, p)
		}
	}
}

func TestRangeStoreContains(t *testing.T) {
	s := NewRangeStore()
	ip := netip.MustParseAddr("20.171.206.9")

	if found, loaded := s.Contains([]string{"openai-gptbot"}, ip); found || loaded {
		t.Fatalf("empty store: found=%v loaded=%v", found, loaded)
	}
	s.Set("openai-gptbot", parseFixture(t, "openai.json"))
	if found, loaded := s.Contains([]string{"openai-gptbot"}, ip); !found || !loaded {
		t.Errorf("inside: found=%v loaded=%v", found, loaded)
	}
	// v4-mapped input is unmapped before matching.
	if found, _ := s.Contains([]string{"openai-gptbot"}, netip.MustParseAddr("::ffff:20.171.206.9")); !found {
		t.Error("v4-mapped address must match its v4 prefix")
	}
	if found, loaded := s.Contains([]string{"openai-gptbot"}, netip.MustParseAddr("8.8.8.8")); found || !loaded {
		t.Errorf("outside: found=%v loaded=%v", found, loaded)
	}
	// One loaded key among several is enough for loaded=true.
	if found, loaded := s.Contains([]string{"missing", "openai-gptbot"}, netip.MustParseAddr("8.8.8.8")); found || !loaded {
		t.Errorf("mixed keys: found=%v loaded=%v", found, loaded)
	}
}

type fakePersister struct{ m map[string][]netip.Prefix }

func (f *fakePersister) LoadRanges(context.Context) (map[string][]netip.Prefix, error) {
	return f.m, nil
}
func (f *fakePersister) SaveRanges(context.Context, string, []netip.Prefix, []string, time.Time) error {
	return nil
}

func TestRangeStoreLoad(t *testing.T) {
	s := NewRangeStore()
	p := &fakePersister{m: map[string][]netip.Prefix{"k": {netip.MustParsePrefix("10.0.0.0/8")}}}
	if err := s.Load(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if found, loaded := s.Contains([]string{"k"}, netip.MustParseAddr("10.2.3.4")); !found || !loaded {
		t.Error("persisted ranges must be usable after Load")
	}
}
