package llmstxt

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGoldens = flag.Bool("update-llms", false, "rewrite testdata goldens")

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGoldens {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("golden %s mismatch (run with -update-llms)\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func acmeSite() Site {
	return Site{
		Name:    "Acme Studio",
		Summary: "Acme Studio builds websites for small firms in Lisbon.",
		Details: Details("Based in Lisbon, Portugal.", "hello@acme.example, +351 21 000 0000.", "Open Mon-Fri 9:00-18:00."),
		BaseURL: "https://acme.example",
		FullOn:  true,
		HasFeed: true,
	}
}

func acmeSections(suffix, locale string) []Section {
	pre := ""
	if locale != "" {
		pre = "/" + locale
	}
	return []Section{
		{Title: "Key pages" + suffix, Locale: locale, Kind: KindKey, Items: []Item{
			{Name: "Home", Path: pre + "/", Desc: "Websites for small firms."},
			{Name: "Services", Path: pre + "/services", Desc: "What we build."},
		}},
		{Title: "Services" + suffix, Locale: locale, Kind: KindType, Items: []Item{
			{Name: "Web design", Path: pre + "/services/web-design", Desc: "Sites that load fast."},
			{Name: "Brand [kit]", Path: pre + "/services/brand-kit"},
		}},
		{Title: "Posts" + suffix, Locale: locale, Kind: KindPosts, Items: []Item{
			{Name: "Launch notes", Path: pre + "/blog/launch", Desc: "How we shipped."},
		}},
		{Title: "People" + suffix, Locale: locale, Kind: KindPeople, Items: []Item{
			{Name: "Elena Voss", Path: pre + "/team/elena-voss", Job: "Lead designer"},
		}},
		{Title: "Pages" + suffix, Locale: locale, Kind: KindPages},
	}
}

func TestIndexGoldens(t *testing.T) {
	checkGolden(t, "index-single.txt", BuildIndex(acmeSite(), acmeSections("", "")))

	multi := append(acmeSections("", ""), acmeSections(" (Français)", "fr")...)
	checkGolden(t, "index-multi.txt", BuildIndex(acmeSite(), multi))

	checkGolden(t, "index-empty.txt", BuildIndex(Site{Name: "acme.example", BaseURL: "https://acme.example/"}, nil))
}

func TestOneLineAndFirstSentence(t *testing.T) {
	if got := OneLine("  a \n b\t c ", 0); got != "a b c" {
		t.Fatalf("OneLine = %q", got)
	}
	if got := OneLine("abcdefghij", 5); got != "abcd…" {
		t.Fatalf("OneLine cap = %q", got)
	}
	if got := FirstSentence("We build sites. Then more.\nText"); got != "We build sites." {
		t.Fatalf("FirstSentence = %q", got)
	}
	if got := FirstSentence("Version 1.5 is out and no stop"); got != "Version 1.5 is out and no stop" {
		t.Fatalf("FirstSentence no stop = %q", got)
	}
	if strings.Contains(string(BuildIndex(Site{Name: "x"}, nil)), "feed.xml") {
		t.Fatal("feed listed without HasFeed")
	}
}
