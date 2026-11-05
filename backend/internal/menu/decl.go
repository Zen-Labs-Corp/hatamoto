package menu

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/core/internal/jsonx"
)

// Limits of a theme's menu locations declaration (theme.json "menus").
const (
	MaxDeclLocations = 12
	MaxDeclItems     = 24
	MaxDeclTitle     = 40
	MaxDeclLabel     = 60
	MaxDeclURL       = 2048
)

var archiveSlugRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// DeclItem is one default item of a declared location: exactly one of URL
// (a site path or http(s) URL) and Archive (a schema slug, resolved to the
// archive path at render time) is set.
type DeclItem struct {
	Label   string
	URL     string
	Archive string
}

// DeclLocation is a menu location a theme declares. Default items are optional;
// without them the location is only listed in the admin picker.
type DeclLocation struct {
	Name    string
	Title   string
	Default []DeclItem
}

// ParseDecl reads the "menus" object of theme.json strictly: any violation is
// an error naming the offending field. An absent or empty value is (nil, nil).
// The result is sorted by location name.
func ParseDecl(raw jsontext.Value) ([]DeclLocation, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var locs map[string]jsontext.Value
	if err := jsonx.Unmarshal(raw, &locs, jsonx.StoredLoose); err != nil {
		return nil, fmt.Errorf("menus: must be an object of locations")
	}
	if len(locs) > MaxDeclLocations {
		return nil, fmt.Errorf("menus: at most %d locations, got %d", MaxDeclLocations, len(locs))
	}
	names := make([]string, 0, len(locs))
	for name := range locs {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []DeclLocation
	for _, name := range names {
		body := locs[name]
		if !locationRe.MatchString(name) {
			return nil, fmt.Errorf("menus: location %q must match %s", name, locationRe)
		}
		loc, err := parseDeclLocation(name, body)
		if err != nil {
			return nil, err
		}
		out = append(out, loc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func parseDeclLocation(name string, body jsontext.Value) (DeclLocation, error) {
	path := "menus." + name
	var fields map[string]jsontext.Value
	if err := jsonx.Unmarshal(body, &fields, jsonx.StoredLoose); err != nil {
		return DeclLocation{}, fmt.Errorf("%s: must be an object", path)
	}
	for k := range fields {
		if k != "title" && k != "default" {
			return DeclLocation{}, fmt.Errorf("%s: unknown key %q", path, k)
		}
	}
	loc := DeclLocation{Name: name, Title: capitalise(name)}
	if t, ok := fields["title"]; ok {
		var title string
		if err := jsonx.Unmarshal(t, &title, jsonx.StoredLoose); err != nil {
			return DeclLocation{}, fmt.Errorf("%s.title: must be a string", path)
		}
		if n := utf8.RuneCountInString(title); n < 1 || n > MaxDeclTitle || hasControl(title) {
			return DeclLocation{}, fmt.Errorf("%s.title: must be 1-%d characters without control characters", path, MaxDeclTitle)
		}
		loc.Title = title
	}
	if d, ok := fields["default"]; ok {
		var items []jsontext.Value
		if err := jsonx.Unmarshal(d, &items, jsonx.StoredLoose); err != nil {
			return DeclLocation{}, fmt.Errorf("%s.default: must be an array", path)
		}
		if len(items) > MaxDeclItems {
			return DeclLocation{}, fmt.Errorf("%s.default: at most %d items, got %d", path, MaxDeclItems, len(items))
		}
		for i, it := range items {
			item, err := parseDeclItem(fmt.Sprintf("%s.default[%d]", path, i), it)
			if err != nil {
				return DeclLocation{}, err
			}
			loc.Default = append(loc.Default, item)
		}
	}
	return loc, nil
}

func parseDeclItem(path string, raw jsontext.Value) (DeclItem, error) {
	var f struct {
		Label   *string `json:"label"`
		URL     *string `json:"url"`
		Archive *string `json:"archive"`
	}
	if err := jsonx.Unmarshal(raw, &f, jsonx.StrictNoUnknown); err != nil {
		return DeclItem{}, fmt.Errorf("%s: %v (allowed keys: label, url, archive)", path, err)
	}
	var it DeclItem
	if f.Label == nil {
		return it, fmt.Errorf("%s.label: required", path)
	}
	if n := utf8.RuneCountInString(*f.Label); n < 1 || n > MaxDeclLabel || hasControl(*f.Label) {
		return it, fmt.Errorf("%s.label: must be 1-%d characters without control characters", path, MaxDeclLabel)
	}
	it.Label = *f.Label
	switch {
	case f.URL != nil && f.Archive != nil:
		return it, fmt.Errorf("%s: set either url or archive, not both", path)
	case f.URL != nil:
		if !validDeclURL(*f.URL) {
			return it, fmt.Errorf("%s: url %q must be a / path or http(s) URL of at most %d characters", path, *f.URL, MaxDeclURL)
		}
		it.URL = *f.URL
	case f.Archive != nil:
		if !archiveSlugRe.MatchString(*f.Archive) {
			return it, fmt.Errorf("%s: archive %q must be a schema slug matching %s", path, *f.Archive, archiveSlugRe)
		}
		it.Archive = *f.Archive
	default:
		return it, fmt.Errorf("%s: needs either url or archive", path)
	}
	return it, nil
}

func validDeclURL(s string) bool {
	if s == "" || len(s) > MaxDeclURL || hasControl(s) {
		return false
	}
	if strings.HasPrefix(s, "/") {
		return !strings.HasPrefix(s, "//")
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
