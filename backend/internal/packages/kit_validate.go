package packages

import (
	"regexp"
	"strconv"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/urlpattern"
	"hatamoto.dev/sdk"
)

// Kit limits (docs/KITS.md "Rules").
const (
	MaxKitSchemas   = 16
	MaxKitPages     = 32
	MaxKitPageBytes = 64 << 10
	MaxKitMenuItems = 24
	MaxKitPlugins   = 8
	MaxKitThemes    = 8
	MaxKitJSON      = 1 << 20
)

const kitFormat = 1

var (
	// kitSlugRe is the slug rule for kit schemas and pages: at most 40 characters.
	kitSlugRe = regexp.MustCompile(`^[a-z]([a-z0-9]|-[a-z0-9]){0,39}$`)
	// kitFeatureRe is a core feature name such as "comment.policy".
	kitFeatureRe = regexp.MustCompile(`^[a-z]+(\.[a-z-]+)+$`)
)

// validate checks kit.json against docs/KITS.md. Unknown `requires` names are
// not refused: apply refuses the ones core lacks.
func (k *Kit) validate() error {
	if !namePattern.MatchString(k.Name) {
		return refuse("name %q is invalid (want %s)", k.Name, namePattern)
	}
	if !semverPattern.MatchString(k.Version) {
		return refuse("version %q is not semver", k.Version)
	}
	if k.KitFormat != kitFormat {
		return refuse("kit: format version %d is not supported (want %d)", k.KitFormat, kitFormat)
	}
	if err := kitRange("sdk", k.SDK); err != nil {
		return err
	}
	for i, r := range k.Requires {
		if !kitFeatureRe.MatchString(r) {
			return refuse("requires[%d]: %q is not a feature name", i, r)
		}
	}
	if err := k.validatePackages(); err != nil {
		return err
	}
	schemas, err := k.validateSchemas()
	if err != nil {
		return err
	}
	pages, err := k.validatePages()
	if err != nil {
		return err
	}
	if err := k.validateMenus(); err != nil {
		return err
	}
	return k.validateSwitches(schemas, pages)
}

func kitRange(field, r string) error {
	if _, err := sdk.Satisfies("0.0.0", r); err != nil {
		return refuse("%s: %q is not a semver range: %v", field, r, err)
	}
	return nil
}

func (k *Kit) validatePackages() error {
	if len(k.Plugins) > MaxKitPlugins {
		return refuse("plugins: at most %d, got %d", MaxKitPlugins, len(k.Plugins))
	}
	if len(k.Themes) > MaxKitThemes {
		return refuse("themes: at most %d, got %d", MaxKitThemes, len(k.Themes))
	}
	seen := map[string]bool{}
	for i, p := range k.Plugins {
		f := "plugins[" + strconv.Itoa(i) + "]"
		if !namePattern.MatchString(p.Name) {
			return refuse("%s.name: %q is invalid (want %s)", f, p.Name, namePattern)
		}
		if seen[p.Name] {
			return refuse("%s.name: %q is listed twice", f, p.Name)
		}
		seen[p.Name] = true
		if err := kitRange(f+".range", p.Range); err != nil {
			return err
		}
		if err := p.Grant.validate(f + ".grant"); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	for i, t := range k.Themes {
		f := "themes[" + strconv.Itoa(i) + "]"
		if !namePattern.MatchString(t.Name) {
			return refuse("%s.name: %q is invalid (want %s)", f, t.Name, namePattern)
		}
		if seen[t.Name] {
			return refuse("%s.name: %q is listed twice", f, t.Name)
		}
		seen[t.Name] = true
		if err := kitRange(f+".range", t.Range); err != nil {
			return err
		}
	}
	return nil
}

// validate checks a list grant; the string form is already checked on decode.
func (g KitGrant) validate(field string) error {
	if g.All || g.Perms == nil {
		return nil
	}
	if len(g.Perms) == 0 {
		return refuse("%s: a grant list must not be empty", field)
	}
	seen := map[string]bool{}
	for i, p := range g.Perms {
		if !sdk.ValidGrant(sdk.Permission(p)) {
			return refuse("%s[%d]: %q is not a permission", field, i, p)
		}
		if seen[p] {
			return refuse("%s[%d]: %q is listed twice", field, i, p)
		}
		seen[p] = true
	}
	return nil
}

func (k *Kit) validateSchemas() (map[string]bool, error) {
	if len(k.Schemas) > MaxKitSchemas {
		return nil, refuse("schemas: at most %d, got %d", MaxKitSchemas, len(k.Schemas))
	}
	slugs := map[string]bool{}
	for i, s := range k.Schemas {
		f := "schemas[" + strconv.Itoa(i) + "]"
		if !kitSlugRe.MatchString(s.Slug) {
			return nil, refuse("%s.slug: %q is invalid (want %s)", f, s.Slug, kitSlugRe)
		}
		if slugs[s.Slug] {
			return nil, refuse("%s.slug: %q is listed twice", f, s.Slug)
		}
		slugs[s.Slug] = true
		if strings.TrimSpace(s.Name) == "" {
			return nil, refuse("%s.name is required", f)
		}
		if s.Schema.Kind() != '{' {
			return nil, refuse("%s.schema must be a JSON object", f)
		}
		if len(s.Settings) == 0 || s.Settings.Kind() == 'n' {
			continue
		}
		if s.Settings.Kind() != '{' {
			return nil, refuse("%s.settings must be a JSON object", f)
		}
		var st struct {
			URLPattern *string `json:"url_pattern"`
		}
		if err := jsonx.Unmarshal(s.Settings, &st, jsonx.Stored); err != nil {
			return nil, refuse("%s.settings: %v", f, err)
		}
		if st.URLPattern != nil {
			if _, err := urlpattern.Parse(*st.URLPattern); err != nil {
				return nil, refuse("%s.settings.url_pattern: %v", f, err)
			}
		}
	}
	return slugs, nil
}

func (k *Kit) validatePages() (map[string]bool, error) {
	if len(k.Pages) > MaxKitPages {
		return nil, refuse("pages: at most %d, got %d", MaxKitPages, len(k.Pages))
	}
	slugs := map[string]bool{}
	homepage := ""
	for i := range k.Pages {
		p := &k.Pages[i]
		f := "pages[" + strconv.Itoa(i) + "]"
		if !kitSlugRe.MatchString(p.Slug) {
			return nil, refuse("%s.slug: %q is invalid (want %s)", f, p.Slug, kitSlugRe)
		}
		if slugs[p.Slug] {
			return nil, refuse("%s.slug: %q is listed twice", f, p.Slug)
		}
		slugs[p.Slug] = true
		if strings.TrimSpace(p.Name) == "" {
			return nil, refuse("%s.name is required", f)
		}
		if p.Schema == "" {
			p.Schema = "page"
		}
		for j, b := range p.Blocks {
			if b.Type == "" {
				return nil, refuse("%s.blocks[%d].type is required", f, j)
			}
		}
		doc, err := jsonx.Marshal(p.Blocks)
		if err != nil {
			return nil, refuse("%s.blocks: %v", f, err)
		}
		if len(doc) > MaxKitPageBytes {
			return nil, refuse("%s: the page document is %d bytes, at most %d", f, len(doc), MaxKitPageBytes)
		}
		if p.Homepage {
			if homepage != "" {
				return nil, refuse("%s.homepage: only one page may ask to be the homepage (%q does already)", f, homepage)
			}
			homepage = p.Slug
		}
	}
	if h := k.Switches.Homepage; h != "" && homepage != "" && h != homepage {
		return nil, refuse("switches.homepage %q differs from the page that asks for it (%q)", h, homepage)
	}
	return slugs, nil
}

func (k *Kit) validateMenus() error {
	seen := map[string]bool{}
	for i, m := range k.Menus {
		f := "menus[" + strconv.Itoa(i) + "]"
		if m.Location == "" || !menu.ValidLocation(m.Location) {
			return refuse("%s.location: %q is not a menu location", f, m.Location)
		}
		if seen[m.Location] {
			return refuse("%s.location: %q is listed twice", f, m.Location)
		}
		seen[m.Location] = true
		if strings.TrimSpace(m.Name) == "" {
			return refuse("%s.name is required", f)
		}
		if len(m.Items) > MaxKitMenuItems {
			return refuse("%s.items: at most %d, got %d", f, MaxKitMenuItems, len(m.Items))
		}
		for j, it := range m.Items {
			if strings.TrimSpace(it.Label) == "" {
				return refuse("%s.items[%d].label is required", f, j)
			}
			local := strings.HasPrefix(it.URL, "/") && !strings.HasPrefix(it.URL, "//")
			if !local && !strings.HasPrefix(it.URL, "https://") {
				return refuse("%s.items[%d].url: %q must start with / or https://", f, j, it.URL)
			}
		}
	}
	return nil
}

func (k *Kit) validateSwitches(schemas, pages map[string]bool) error {
	for i, s := range k.Switches.CommentPolicy {
		if !schemas[s] {
			return refuse("switches.comment_policy[%d]: %q is not a schema of this kit", i, s)
		}
	}
	if h := k.Switches.Homepage; h != "" && !pages[h] {
		return refuse("switches.homepage: %q is not a page of this kit", h)
	}
	return nil
}

// checkKitSize refuses a kit.json above MaxKitJSON.
func checkKitSize(b []byte) error {
	if len(b) > MaxKitJSON {
		return refuse("%s is larger than %d bytes", KitManifest, MaxKitJSON)
	}
	return nil
}
