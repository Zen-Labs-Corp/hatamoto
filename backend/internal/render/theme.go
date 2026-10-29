package render

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"hatamoto.dev/core/internal/builder"
)

// SectionDef is one section type of a theme: its template source and schema.
type SectionDef struct {
	Type string
	// Template is the html/template source of sections/<type>.html.
	Template string
	// Schema is the raw JSON Schema of schemas/<type>.json (nil if absent).
	Schema json.RawMessage
	// Meta is the schema's x-hm-section extension.
	Meta SectionMeta
	// Aliases maps a canonical top-level field to its legacy names (x-hm-alias).
	Aliases map[string][]string
	// FieldKinds maps a top-level field to its x-hm-field kind.
	FieldKinds map[string]string
}

// Theme is a loaded, validated theme resolved through a Chain.
type Theme struct {
	Name    string
	Version string
	// Settings is the raw settings_schema from theme.json (may be nil).
	Settings json.RawMessage
	// Sections is keyed by section type, discovered from the file layout.
	Sections map[string]*SectionDef

	// BuilderTokens is the theme's opt-in (theme.json "builder": {"tokens": true})
	// to the site design tokens: the :root token block and the --hm-fs-* type
	// scale variables. Without it the builder emits neither.
	BuilderTokens bool
	// BuilderParts are the layout slots (header, footer, single, archive) the
	// theme lets the owner fill with a part built in the builder
	// (theme.json builder.parts); nil when it did not opt in.
	BuilderParts map[string]bool
	// StrictScripts is the theme's opt-in (theme.json "csp": {"strictScripts": true})
	// to the public policy without 'unsafe-inline' in script-src. It promises the
	// theme emits no inline executable script and no on*= handler on public pages.
	StrictScripts bool
	// Menu is what the theme declares for menu items (theme.json "menu"):
	// style, colour, badge and icon presets and suggested classes. Zero when
	// the theme declares nothing.
	Menu MenuDecl

	chain Chain
}

type themeManifest struct {
	Name           string          `json:"name"`
	Version        string          `json:"version"`
	SettingsSchema json.RawMessage `json:"settings_schema"`
	Builder        struct {
		Tokens bool     `json:"tokens"`
		Parts  []string `json:"parts"`
	} `json:"builder"`
	CSP struct {
		StrictScripts bool `json:"strictScripts"`
	} `json:"csp"`
	Menu json.RawMessage `json:"menu"`
}

// LoadTheme resolves theme.json (highest-precedence copy) and discovers section
// types from sections/*.html and schemas/*.json across the chain. There is no
// central type list. A schema without a template is an error.
func LoadTheme(c Chain) (*Theme, error) {
	t := &Theme{Sections: map[string]*SectionDef{}, chain: c}

	if b, err := c.ReadFile("theme.json"); err == nil {
		var m themeManifest
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("theme.json: %w", err)
		}
		t.Name, t.Version, t.Settings = m.Name, m.Version, m.SettingsSchema
		t.BuilderTokens = m.Builder.Tokens
		t.StrictScripts = m.CSP.StrictScripts
		t.Menu = parseMenuDecl(m.Menu)
		for _, p := range m.Builder.Parts {
			if builder.ValidPartSlot(p) { // unknown names are ignored
				if t.BuilderParts == nil {
					t.BuilderParts = map[string]bool{}
				}
				t.BuilderParts[p] = true
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("theme.json: %w", err)
	}
	if len(t.Settings) > 0 {
		if err := validateSchemaSubset(t.Settings); err != nil {
			return nil, fmt.Errorf("theme.json settings_schema: %w", err)
		}
	}

	templates, err := c.Glob("sections/*.html")
	if err != nil {
		return nil, err
	}
	for _, p := range templates {
		typ := strings.TrimSuffix(path.Base(p), ".html")
		src, err := c.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		t.Sections[typ] = &SectionDef{Type: typ, Template: string(src)}
	}

	schemas, err := c.Glob("schemas/*.json")
	if err != nil {
		return nil, err
	}
	for _, p := range schemas {
		typ := strings.TrimSuffix(path.Base(p), ".json")
		def, ok := t.Sections[typ]
		if !ok {
			return nil, fmt.Errorf("%s: schema has no sections/%s.html template", p, typ)
		}
		raw, err := c.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		def.Schema = raw
		def.Meta, def.Aliases, def.FieldKinds, err = parseSectionSchema(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	return t, nil
}

// VersionHash returns a short content hash over the name and bytes of every
// effective file in the chain (precedence applied), so it changes whenever any
// layer's visible output could change.
func (t *Theme) VersionHash() string {
	return chainHash(t.chain)
}

func chainHash(c Chain) string {
	names, err := c.Files()
	if err != nil {
		return ""
	}
	h := sha256.New()
	for _, n := range names {
		b, err := c.ReadFile(n)
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s\x00%d\x00", n, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}
