package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// converterVersion is part of every stored source hash. Bump it whenever a
// change to the converter or to the stored attribute shape should reach pages
// whose source file did not change.
const converterVersion = "1"

// StampHashes replaces each page's file hash with a hash of everything the
// stored entity is built from: the converter version, the file bytes, the
// page's parent path under the manifest root and the converted HTML (which
// carries the resolved link targets). A moved or renamed target page, a
// changed manifest root or a converter fix therefore updates the pages it
// affects, while an unchanged tree still hashes the same on every run. A page
// that does not convert keeps its file hash; the lint reports it.
func StampHashes(pages []Page, m Manifest) {
	render, err := renderer(pages, m)
	if err != nil {
		return
	}
	for i := range pages {
		html, err := render(pages[i])
		if err != nil {
			continue
		}
		pages[i].Hash = stampHash(converterVersion, pages[i], parentPath(m, pages[i]), html)
	}
}

// parentPath is the site path of the page's parent entity: the manifest root
// for a section index, the section path for a page in it.
func parentPath(m Manifest, p Page) string {
	base := "/" + strings.Trim(m.Root, "/")
	if p.Section == "" || p.Slug == IndexSlug {
		return base
	}
	return base + "/" + p.Section
}

func stampHash(version string, p Page, parent, html string) string {
	h := sha256.New()
	for _, part := range []string{version, p.Hash, parent, html} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
