package packages

import (
	"fmt"
	"io/fs"
	"reflect"
	"regexp"
	"strings"

	"hatamoto.dev/core/internal/tags"
)

// A tag pack is a plugin package that holds only plugin.json and
// tags/<stem>.json: declarations of third-party tags core loads itself, after
// consent, with only the declared hosts in the page policy. It carries no
// code, so it needs no permissions and may come from a community author.
const (
	tagsDir     = "tags"
	maxPackTags = 4 // tag files in one package
	// maxTagFile bounds one declaration; real ones are under 1 KiB.
	maxTagFile = 16 << 10
)

var tagStemRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// checkTagEntry refuses a path under tags/ that is not tags/ itself or
// tags/<stem>.json.
func checkTagEntry(clean string, isDir bool) error {
	parts := strings.Split(clean, "/")
	if len(parts) == 1 {
		return nil // the directory (a file named tags was refused as a root file)
	}
	stem, ok := strings.CutSuffix(parts[1], ".json")
	if len(parts) != 2 || isDir || !ok || !tagStemRe.MatchString(stem) {
		return refuse("%s: a tag pack holds only tags/<name>.json, the name lower-case letters, digits and \"-\", at most 31", clean)
	}
	return nil
}

// tagPackContentError reports the first thing a tag pack may not hold next to
// tags/ (one of the other package directories, or plugin.wasm).
func tagPackContentError(other string, hasWasm bool) error {
	found := other
	if hasWasm {
		found = PluginWasm
	}
	if found == "" {
		return nil
	}
	return refuse("tags/: a tag pack holds only %s and tags/*.json (found %s)", PluginManifest, found)
}

// tagPackManifestError refuses a manifest that asks for anything beyond
// identity: a tag pack gets no permissions, hosts, routes, CSP or settings.
func (p *Package) tagPackManifestError() error {
	m, err := p.PluginManifest()
	if err != nil {
		return refuse("%s: %v", PluginManifest, err)
	}
	v := reflect.ValueOf(m)
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		switch name {
		case "Name", "Version", "SDK", "Description":
			continue
		}
		f := v.Field(i)
		if f.IsZero() || (f.Kind() == reflect.Slice || f.Kind() == reflect.Map) && f.Len() == 0 {
			continue
		}
		return refuse("%s: a tag pack declares only name, version, sdk and description (found %s)", PluginManifest, name)
	}
	return nil
}

// TagVendors reads and validates the tag declarations of a plugin package, in
// file name order; none for a package without tags/. Errors read
// "tags/<file>: <rule>".
func (p *Package) TagVendors() ([]tags.Vendor, error) {
	if p.Kind != KindPlugin {
		return nil, nil
	}
	entries, err := fs.ReadDir(p.FS(), tagsDir)
	if err != nil {
		return nil, nil // no tags/
	}
	if len(entries) > maxPackTags {
		return nil, fmt.Errorf("tags/: a package holds at most %d tag files", maxPackTags)
	}
	var out []tags.Vendor
	for _, e := range entries { // ReadDir sorts by name
		name := tagsDir + "/" + e.Name()
		raw, err := p.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		if len(raw) > maxTagFile {
			return nil, fmt.Errorf("%s: larger than %d bytes", name, maxTagFile)
		}
		v, err := tags.ParseDecl(p.Name, strings.TrimSuffix(e.Name(), ".json"), raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// validateTagPack runs the tag rules; Install and reconcile both call it.
func validateTagPack(p *Package) error {
	_, err := p.TagVendors()
	return err
}

// TagSummaryLine describes the tags of a package for the pack command, for
// example "1 tag: hotjar, hosts 2, category analytics". Empty without tags.
func TagSummaryLine(p *Package) (string, error) {
	vs, err := p.TagVendors()
	if err != nil || len(vs) == 0 {
		return "", err
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		_, stem, _ := strings.Cut(v.ID, ".")
		parts[i] = fmt.Sprintf("%s, hosts %d, category %s", stem, len(v.Script), v.Category)
	}
	noun := "tag"
	if len(vs) > 1 {
		noun = "tags"
	}
	return fmt.Sprintf("%d %s: %s", len(vs), noun, strings.Join(parts, "; ")), nil
}

// tagSummaries lists the vendors of a tag pack the way a registry entry does.
func tagSummaries(vs []tags.Vendor) []TagSummary {
	var out []TagSummary
	for _, v := range vs {
		hosts := make([]string, len(v.Script))
		for i, h := range v.Script {
			hosts[i] = strings.TrimPrefix(h, "https://")
		}
		out = append(out, TagSummary{Name: v.Label, Category: v.Category, Hosts: hosts})
	}
	return out
}
