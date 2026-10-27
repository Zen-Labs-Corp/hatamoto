package main

import "testing"

func stamped(t *testing.T, a string, root string) []Page {
	t.Helper()
	pages, m := contentSite(a, "## Real\n")
	for i := range pages {
		pages[i].Hash = "file-" + pages[i].Path
	}
	m.Root = root
	StampHashes(pages, m)
	return pages
}

func TestStampHashIsStableAndSeesLinkTargets(t *testing.T) {
	t.Parallel()
	a1 := stamped(t, "[x](b.md)\n", "docs")
	a2 := stamped(t, "[x](b.md)\n", "docs")
	for i := range a1 {
		if a1[i].Hash != a2[i].Hash || a1[i].Hash == "file-"+a1[i].Path {
			t.Fatalf("%s: hash must be stable and stamped: %q %q", a1[i].Path, a1[i].Hash, a2[i].Hash)
		}
	}
	// Same file bytes, different resolved link target (manifest root moved):
	// the page that links must change, the one that does not must not.
	moved := stamped(t, "[x](b.md)\n", "handbook")
	if a1[2].Hash == moved[2].Hash {
		t.Error("a page whose link target moved kept its hash")
	}
	if a1[3].Hash == moved[3].Hash {
		t.Error("the parent path is part of the hash, so a moved root must change every page")
	}
}

func TestStampHashSeesConverterVersion(t *testing.T) {
	t.Parallel()
	p := Page{Section: "s", Slug: "a", Hash: "file"}
	if stampHash("1", p, "/docs/s", "<p>x</p>") == stampHash("2", p, "/docs/s", "<p>x</p>") {
		t.Fatal("converter version must change the hash")
	}
	if stampHash("1", p, "/docs/s", "<p>x</p>") == stampHash("1", p, "/docs/t", "<p>x</p>") {
		t.Fatal("parent path must change the hash")
	}
	if stampHash("1", p, "/docs/s", "<p>x</p>") == stampHash("1", p, "/docs/s", "<p>y</p>") {
		t.Fatal("converted html must change the hash")
	}
}

func TestStampHashSeesNextTargets(t *testing.T) {
	t.Parallel()
	hashOf := func(next ...string) string {
		pages, m := contentSite("## A\n", "## B\n")
		pages[2].Next = next
		for i := range pages {
			pages[i].Hash = "file"
		}
		StampHashes(pages, m)
		return pages[2].Hash
	}
	if hashOf("b.md", "index.md") == hashOf("index.md", "b.md") {
		t.Error("the order of the next targets must change the hash")
	}
	if hashOf("b.md", "index.md") == hashOf() {
		t.Error("removing next must change the hash")
	}
	if first, second := hashOf("b.md", "index.md"), hashOf("b.md", "index.md"); first != second {
		t.Error("hash must be stable")
	}
}
