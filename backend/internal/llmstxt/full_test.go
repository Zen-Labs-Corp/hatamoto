package llmstxt

import (
	"fmt"
	"strings"
	"testing"
)

func acmePages() []Page {
	return []Page{
		{Name: "Home", Path: "/", Updated: "2026-11-20", Body: "Websites for small firms."},
		{Name: "Services", Path: "/services", Updated: "2026-11-21", Body: "## What we build\n\n- Sites\n- Shops"},
		{Name: "Empty page", Path: "/empty", Updated: "2026-11-22"},
	}
}

func TestFullGolden(t *testing.T) {
	w := NewFullWriter(acmeSite(), 2<<20)
	for _, p := range acmePages() {
		if !w.Add(p) {
			t.Fatalf("page %s refused", p.Name)
		}
	}
	out, st := w.Finish(0)
	if st.Truncated || st.Pages != 3 || st.Left != 0 || st.Bytes != len(out) {
		t.Fatalf("stats = %+v", st)
	}
	checkGolden(t, "full.txt", out)
}

func TestFullTruncated(t *testing.T) {
	const capBytes = 1024
	w := NewFullWriter(acmeSite(), capBytes)
	body := strings.Repeat("word ", 50) // ~250 bytes per block
	added := 0
	for i := 1; i <= 5; i++ {
		if w.Add(Page{Name: fmt.Sprintf("Page %d", i), Path: fmt.Sprintf("/p%d", i), Updated: "2026-11-22", Body: body}) {
			added++
		}
	}
	out, st := w.Finish(0)
	if len(out) > capBytes {
		t.Fatalf("output %d bytes exceeds cap %d", len(out), capBytes)
	}
	if !st.Truncated || st.Pages != added || st.Pages+st.Left != 5 || st.Left == 0 {
		t.Fatalf("stats = %+v (added %d)", st, added)
	}
	want := fmt.Sprintf("This file was truncated at 1 KB. %d more pages are left out; see https://acme.example/sitemap.xml for the full list.\n", st.Left)
	if !strings.HasSuffix(string(out), "\n"+want) {
		t.Fatalf("missing truncation line %q in:\n%s", want, out)
	}
	if strings.Count(string(out), "\n# Page ") != added {
		t.Fatalf("a block was cut or dropped:\n%s", out)
	}
	checkGolden(t, "full-truncated.txt", out)
}

func TestFullRemainingNotLoaded(t *testing.T) {
	w := NewFullWriter(Site{Name: "Acme Studio", BaseURL: "https://acme.example"}, 2<<20)
	w.Add(Page{Name: "Home", Path: "/", Updated: "2026-11-20"})
	out, st := w.Finish(7)
	if !st.Truncated || st.Left != 7 || !strings.Contains(string(out), "7 more pages") {
		t.Fatalf("stats = %+v\n%s", st, out)
	}
}
