package server

import (
	"encoding/xml"
	"strings"
	"testing"
)

func marshalURLSet(t *testing.T, urls []URL) string {
	t.Helper()
	set := URLSet{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: urls}
	if sitemapHasImages(urls) {
		set.XMLNSImage = sitemapImageNS
	}
	out, err := xml.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestSitemapXMLPriority(t *testing.T) {
	got := marshalURLSet(t, []URL{
		{Loc: "https://x.test/a", Priority: ptr(0.8)},
		{Loc: "https://x.test/zero", Priority: ptr(0.0)},
		{Loc: "https://x.test/none"},
	})
	if strings.Count(got, "<priority>") != 2 || !strings.Contains(got, "<priority>0</priority>") || !strings.Contains(got, "<priority>0.8</priority>") {
		t.Errorf("priority elements wrong: %s", got)
	}
}

func TestSitemapXMLImageNamespaceOnlyWithImages(t *testing.T) {
	plain := marshalURLSet(t, []URL{{Loc: "https://x.test/a"}})
	if strings.Contains(plain, "xmlns:image") || strings.Contains(plain, "image:") {
		t.Errorf("image namespace declared without images: %s", plain)
	}
	with := marshalURLSet(t, []URL{{Loc: "https://x.test/a", Images: []ImageRef{{Loc: "https://x.test/i.png"}}}})
	for _, want := range []string{`xmlns:image="` + sitemapImageNS + `"`, "<image:image><image:loc>https://x.test/i.png</image:loc></image:image>"} {
		if !strings.Contains(with, want) {
			t.Errorf("missing %q in %s", want, with)
		}
	}
}
