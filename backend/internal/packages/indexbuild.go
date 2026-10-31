package packages

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// DefaultIndexValidity is how long a freshly built registry index is believed.
const DefaultIndexValidity = 90 * 24 * time.Hour

// BuildIndex lists every .hmp in dir as a registry Entry whose URL is
// urlPrefix + file name (a relative prefix such as "packages/" keeps the
// registry movable), and wraps the index in an envelope signed with key. It is
// what `hatamoto registry index` runs. Packages are listed by name.
//
// validity must be positive and at most maxIndexLifetime (180 days); the window is
// counted from issued_at (one minute before now, to forgive clock skew), so the
// longest index still passes the freshness check servers apply.
func BuildIndex(dir, urlPrefix string, key Key, now time.Time, validity time.Duration) ([]byte, error) {
	return BuildIndexWith(dir, urlPrefix, key, now, validity, BuildOptions{})
}

// Meta says where a package came from. It is keyed by package name in
// BuildOptions.Meta and never changes the package's identity.
type Meta struct {
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"`
	Source string `json:"source,omitempty"`
	Commit string `json:"commit,omitempty"`
	Tier   string `json:"tier"`
	// Screenshot is screenshots/<name>.(png|webp|jpg); themes only.
	Screenshot string `json:"screenshot,omitempty"`
	// About and Features: see Entry; the caps are the same as the build scripts.
	About    string   `json:"about,omitempty"`
	Features []string `json:"features,omitempty"`
	// Category must be one of Categories; Featured pins the package on Browse.
	Category string `json:"category,omitempty"`
	Featured bool   `json:"featured,omitempty"`
	// SiteType must be one of SiteTypes and Flags any of ThemeFlags; themes only.
	SiteType string   `json:"site_type,omitempty"`
	Flags    []string `json:"flags,omitempty"`
}

// BuildOptions are the optional inputs of BuildIndexWith.
type BuildOptions struct {
	Meta map[string]Meta
}

const maxMetaTitle = 60

var (
	metaCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	metaAuthorPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._@-]{0,60}$`)
)

// ParseMeta reads a JSON object of package name to Meta. Unknown fields are
// refused so a typo cannot silently drop a value.
func ParseMeta(raw []byte) (map[string]Meta, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m map[string]Meta
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("meta: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("meta: trailing data after the JSON object")
	}
	return m, nil
}

func (m Meta) validate(name string, kind Kind) error {
	if m.Tier != TierFirstParty && m.Tier != TierCommunity {
		return fmt.Errorf("tier %q must be %s or %s", m.Tier, TierFirstParty, TierCommunity)
	}
	if m.Title != "" {
		// The rule is shared with build-community.sh and the plugins repo
		// validator: 1 to 60 characters, no control characters, no leading or
		// trailing space.
		if n := utf8.RuneCountInString(m.Title); n > maxMetaTitle || strings.HasPrefix(m.Title, " ") || strings.HasSuffix(m.Title, " ") {
			return fmt.Errorf("title must be 1 to %d characters without surrounding spaces", maxMetaTitle)
		}
		for _, r := range m.Title {
			if unicode.IsControl(r) {
				return fmt.Errorf("title %q has a control character", m.Title)
			}
		}
	}
	if m.About != "" {
		if err := validateAbout(m.About); err != nil {
			return fmt.Errorf("about: %w", err)
		}
	}
	if m.Features != nil {
		if err := validateFeatures(m.Features); err != nil {
			return fmt.Errorf("features: %w", err)
		}
	}
	if m.Category != "" && !ValidCategory(m.Category) {
		return fmt.Errorf("category %q must be one of %s", m.Category, strings.Join(Categories, ", "))
	}
	if m.SiteType != "" || len(m.Flags) > 0 {
		if kind != KindTheme {
			return fmt.Errorf("site_type and flags are only allowed for themes")
		}
	}
	if m.SiteType != "" && !ValidSiteType(m.SiteType) {
		return fmt.Errorf("site_type %q must be one of %s", m.SiteType, strings.Join(SiteTypes, ", "))
	}
	for _, f := range m.Flags {
		if !ValidThemeFlag(f) {
			return fmt.Errorf("flag %q must be one of %s", f, strings.Join(ThemeFlags, ", "))
		}
	}
	if m.Author != "" && !metaAuthorPattern.MatchString(m.Author) {
		return fmt.Errorf("author %q is not allowed", m.Author)
	}
	if m.Commit != "" && !metaCommitPattern.MatchString(m.Commit) {
		return fmt.Errorf("commit %q must be 40 lowercase hex characters", m.Commit)
	}
	if m.Source != "" {
		u, err := url.Parse(m.Source)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return fmt.Errorf("source %q must be an https URL without credentials", m.Source)
		}
	}
	if m.Screenshot != "" {
		if kind != KindTheme {
			return fmt.Errorf("screenshot is only allowed for themes")
		}
		if !regexp.MustCompile(`^screenshots/` + regexp.QuoteMeta(name) + `\.(png|webp|jpg)$`).MatchString(m.Screenshot) {
			return fmt.Errorf("screenshot %q must be screenshots/%s.png, .webp or .jpg", m.Screenshot, name)
		}
	}
	return nil
}

// proseProblem names what is wrong with a line of about/features text, or "".
func proseProblem(s string) string {
	switch {
	case strings.ContainsAny(s, "<>"):
		return "must not contain < or >"
	case strings.Contains(s, "://") || strings.Contains(strings.ToLower(s), "www."):
		return "must not contain a URL"
	case s != strings.TrimSpace(s):
		return "must not start or end with whitespace"
	case strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Cf, r) }) >= 0:
		return "must not contain invisible format characters (zero-width, bidi override)"
	}
	return ""
}

func validateAbout(about string) error {
	if n := utf8.RuneCountInString(about); n > maxAbout {
		return fmt.Errorf("must be 1 to %d characters, got %d", maxAbout, n)
	}
	if hasControl(about, '\n') {
		return fmt.Errorf("must not contain control characters other than a newline between paragraphs")
	}
	if p := proseProblem(about); p != "" {
		return fmt.Errorf("%s", p)
	}
	paras := strings.Split(about, "\n")
	if len(paras) > maxAboutParas {
		return fmt.Errorf("must have at most %d paragraphs", maxAboutParas)
	}
	for _, p := range paras {
		if p == "" {
			return fmt.Errorf("must not have an empty paragraph")
		}
		if strings.TrimSpace(p) != p {
			return fmt.Errorf("a paragraph must not start or end with whitespace")
		}
	}
	return nil
}

func validateFeatures(features []string) error {
	if len(features) == 0 || len(features) > maxFeatures {
		return fmt.Errorf("must list 1 to %d items", maxFeatures)
	}
	seen := make(map[string]bool, len(features))
	for _, f := range features {
		if n := utf8.RuneCountInString(f); n < 1 || n > maxFeature {
			return fmt.Errorf("item %q must be 1 to %d characters", f, maxFeature)
		}
		if hasControl(f, -1) {
			return fmt.Errorf("item %q has a control character", f)
		}
		if p := proseProblem(f); p != "" {
			return fmt.Errorf("item %q %s", f, p)
		}
		if seen[f] {
			return fmt.Errorf("item %q is listed twice", f)
		}
		seen[f] = true
	}
	return nil
}

// BuildIndexWith is BuildIndex plus optional per-package metadata. Package
// names must be unique per kind (a theme and a plugin may share one). Meta is
// keyed by bare name: every Meta name must match a package in dir, and a name
// carried by both a plugin and a theme cannot take Meta.
func BuildIndexWith(dir, urlPrefix string, key Key, now time.Time, validity time.Duration, opts BuildOptions) ([]byte, error) {
	if validity <= 0 || validity > maxIndexLifetime {
		return nil, fmt.Errorf("an index must be valid for more than 0 and at most %d days, not %s", int(maxIndexLifetime.Hours()/24), validity)
	}
	issued := now.UTC().Add(-time.Minute)
	files, err := filepath.Glob(filepath.Join(dir, "*"+Extension))
	if err != nil {
		return nil, err
	}
	idx := Index{
		Version:   1,
		IssuedAt:  issued.Format(time.RFC3339),
		ExpiresAt: issued.Add(validity).Format(time.RFC3339),
	}
	type ident struct{ kind, name string }
	seen := map[ident]string{}
	perName := map[string]int{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		p, err := Parse(raw, DefaultLimits)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		id := ident{string(p.Kind), p.Name}
		if prev, dup := seen[id]; dup {
			return nil, fmt.Errorf("%s: package %q is already provided by %s", f, p.Name, prev)
		}
		seen[id] = f
		perName[p.Name]++
		e := Entry{Kind: p.Kind, Name: p.Name, Version: p.Version, URL: urlPrefix + filepath.Base(f), Size: int64(len(raw))}
		sum := p.SHA256()
		e.SHA256 = hex.EncodeToString(sum[:])
		switch p.Kind {
		case KindPlugin:
			m, err := p.PluginManifest()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			e.Description, e.Permissions = m.Description, m.Permissions
		case KindTheme:
			m, err := p.ThemeMeta()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			e.Description = m.Description
		}
		if m, ok := opts.Meta[p.Name]; ok {
			if err := m.validate(p.Name, p.Kind); err != nil {
				return nil, fmt.Errorf("meta for %s: %w", p.Name, err)
			}
			e.Title, e.Author, e.Source, e.Commit, e.Tier, e.Screenshot = m.Title, m.Author, m.Source, m.Commit, m.Tier, m.Screenshot
			e.About, e.Features = m.About, m.Features
			e.Category, e.Featured = m.Category, m.Featured
			e.SiteType, e.Flags = m.SiteType, cleanFlags(m.Flags)
		}
		idx.Packages = append(idx.Packages, e)
	}
	for name := range opts.Meta {
		switch perName[name] {
		case 0:
			return nil, fmt.Errorf("meta names %q, which is not a package in %s", name, dir)
		case 1:
		default:
			return nil, fmt.Errorf("meta names %q, which is both a plugin and a theme in %s", name, dir)
		}
	}
	sort.Slice(idx.Packages, func(i, j int) bool { return idx.Packages[i].Name < idx.Packages[j].Name })
	body, err := json.Marshal(idx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(urlPrefix) == "" && len(idx.Packages) > 0 {
		return nil, fmt.Errorf("registry: url prefix is empty")
	}
	return SignIndex(body, key)
}
