package packages

import (
	"encoding/json/jsontext"
	"errors"
	"fmt"

	"hatamoto.dev/core/internal/jsonx"
)

// Kit is kit.json (docs/KITS.md). Field names are the frozen contract.
type Kit struct {
	Name        string       `json:"name"`
	Version     string       `json:"version"`
	KitFormat   int          `json:"kit"`
	SDK         string       `json:"sdk"`
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	Requires    []string     `json:"requires,omitzero"`
	Plugins     []KitPackage `json:"plugins,omitzero"`
	Themes      []KitTheme   `json:"themes,omitzero"`
	Schemas     []KitSchema  `json:"schemas,omitzero"`
	Pages       []KitPage    `json:"pages,omitzero"`
	Menus       []KitMenu    `json:"menus,omitzero"`
	Switches    KitSwitches  `json:"switches,omitzero"`
}

// KitPackage is a plugin a kit asks for.
type KitPackage struct {
	Name  string   `json:"name"`
	Range string   `json:"range"`
	Grant KitGrant `json:"grant,omitzero"`
}

// KitGrant is docs/KITS.md `plugins[].grant`: the string "all", a list of
// permission names, or absent (the owner ticks each permission). A list that
// is present but empty decodes to a non-nil empty Perms so validation can
// refuse it.
type KitGrant struct {
	All   bool
	Perms []string
}

// UnmarshalJSONFrom reads "all" or an array of permission names. Any other
// string is refused here.
func (g *KitGrant) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	v, err := dec.ReadValue()
	if err != nil {
		return err
	}
	switch v.Kind() {
	case 'n':
		*g = KitGrant{}
	case '"':
		var s string
		if err := jsonx.Unmarshal(v, &s); err != nil {
			return err
		}
		if s != "all" {
			return fmt.Errorf("grant %q: a string grant must be \"all\"", s)
		}
		*g = KitGrant{All: true}
	case '[':
		perms := []string{}
		if err := jsonx.Unmarshal(v, &perms); err != nil {
			return err
		}
		*g = KitGrant{Perms: perms}
	default:
		return errors.New("grant must be \"all\" or a list of permission names")
	}
	return nil
}

// MarshalJSONTo writes "all", the list, or null for an absent grant.
func (g KitGrant) MarshalJSONTo(enc *jsontext.Encoder) error {
	var (
		b   []byte
		err error
	)
	switch {
	case g.All:
		b = []byte(`"all"`)
	case g.Perms != nil:
		b, err = jsonx.Marshal(g.Perms)
	default:
		b = []byte("null")
	}
	if err != nil {
		return err
	}
	return enc.WriteValue(b)
}

// KitTheme is a theme a kit asks for.
type KitTheme struct {
	Name     string `json:"name"`
	Range    string `json:"range"`
	Activate bool   `json:"activate,omitzero"`
}

// KitSchema is a content type a kit creates.
type KitSchema struct {
	Slug     string         `json:"slug"`
	Name     string         `json:"name"`
	Schema   jsontext.Value `json:"schema"`
	Settings jsontext.Value `json:"settings,omitzero"`
}

// KitPage is a page a kit creates.
type KitPage struct {
	Slug     string     `json:"slug"`
	Name     string     `json:"name"`
	Schema   string     `json:"schema,omitempty"`
	Blocks   []KitBlock `json:"blocks,omitzero"`
	Homepage bool       `json:"homepage,omitzero"`
}

// KitBlock is one block of a kit page.
type KitBlock struct {
	Type  string         `json:"type"`
	Props map[string]any `json:"props,omitzero"`
}

// KitMenu is a menu a kit creates at a theme location.
type KitMenu struct {
	Location string        `json:"location"`
	Name     string        `json:"name"`
	Items    []KitMenuItem `json:"items,omitzero"`
}

// KitMenuItem is one link of a kit menu.
type KitMenuItem struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// KitSwitches are requests applied only with the owner's ticks.
type KitSwitches struct {
	Comments      bool     `json:"comments,omitzero"`
	CommentPolicy []string `json:"comment_policy,omitzero"`
	Homepage      string   `json:"homepage,omitempty"`
}

// parseKit decodes kit.json strictly: an unknown key refuses the kit.
func parseKit(b []byte) (*Kit, error) {
	var k Kit
	if err := jsonx.Unmarshal(b, &k, jsonx.StrictNoUnknown); err != nil {
		return nil, refuse("%v", err)
	}
	return &k, nil
}

// DisplayTitle returns Title, or Name when Title is empty.
func (k *Kit) DisplayTitle() string {
	if k.Title != "" {
		return k.Title
	}
	return k.Name
}
