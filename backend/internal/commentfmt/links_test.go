package commentfmt

import (
	"reflect"
	"strings"
	"testing"
)

func TestFindLinks(t *testing.T) {
	t.Parallel()
	positives := []string{
		"see https://example.com/x",
		"www.example.com",
		"example.com",
		"Example.COM/path",
		"[here](http://example.com)",
		"hxxps://example.com",
		"h**p://example.com",
		"example[.]com",
		"example (.) com",
		"example{.}com",
		"example dot com",
		"example [dot] com",
		"mail me at sub.example.org",
		"ftp://example.org/file",
		// Accepted false positive: .sh is a country-code TLD, so this is
		// held for review rather than lost.
		"run config.sh",
	}
	for _, s := range positives {
		if !HasLinks(s) {
			t.Errorf("HasLinks(%q) = false, want true", s)
		}
	}
	negatives := []string{
		"e.g. this", "i.e. that", "version 1.2.3", "file.txt", "Node.js rocks",
		"3.14", "ellipsis... done", "",
	}
	for _, s := range negatives {
		if l := FindLinks(s); len(l) != 0 {
			t.Errorf("FindLinks(%q) = %+v, want none", s, l)
		}
	}
}

func TestFindLinksLinkable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		linkable bool
		domain   string
	}{
		{"https://example.com/a", true, "example.com"},
		{"www.example.com", true, "example.com"},
		{"example.com", false, "example.com"},
		{"hxxps://example.com", false, "example.com"},
		{"ftp://example.com/f", false, "example.com"},
		{"javascript://example.com", false, "example.com"},
		{"example [dot] com", false, "example.com"},
		{"[t](https://example.org)", true, "example.org"},
		{"[t](ftp://example.org)", false, "example.org"},
	}
	for _, c := range cases {
		l := FindLinks(c.in)
		if len(l) != 1 || l[0].Linkable != c.linkable || l[0].Domain != c.domain {
			t.Errorf("FindLinks(%q) = %+v, want one link linkable=%v domain=%q", c.in, l, c.linkable, c.domain)
		}
	}
}

func TestFindLinksTrimsTrailingPunctuation(t *testing.T) {
	t.Parallel()
	in := "go to https://example.com/a."
	l := FindLinks(in)
	if len(l) != 1 || in[l[0].Start:l[0].End] != "https://example.com/a" {
		t.Fatalf("got %+v", l)
	}
}

func TestFindLinksCapsInput(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("a ", maxScan) + "example.com"
	if HasLinks(s) {
		t.Fatal("link beyond the scan cap was reported")
	}
}

func TestDomains(t *testing.T) {
	t.Parallel()
	got := Domains(FindLinks("https://WWW.Example.com/a and example.com"))
	if want := []string{"example.com"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Domains = %v, want %v", got, want)
	}
	got = Domains(FindLinks("b.example.org a.example.com"))
	if want := []string{"a.example.com", "b.example.org"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Domains = %v, want %v", got, want)
	}
}
