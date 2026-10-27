package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/builder"
)

// Stable machine prefixes of builder tool errors. The MCP error is plain text,
// so an agent tells the cases apart by the text before the first colon.
const (
	codeConflict       = "conflict"         // stale version; the text names the current one
	codeInvalidPageDoc = "invalid_page_doc" // the document or an op was refused
	codeNotBuilder     = "not_builder"      // the entity has no builder layout
	codeLimit          = "limit"            // a size limit was exceeded
	codeUnavailable    = "unavailable"      // the site lacks a service the tool needs
)

// codedErr is an error for the agent that starts with a stable code.
func codedErr(code, format string, a ...any) error {
	return userErr("%s: %s", code, fmt.Sprintf(format, a...))
}

// store is the entity service the builder tools write through.
func (s *Server) store() builderStore {
	if s.d.BuilderEntities != nil {
		return s.d.BuilderEntities
	}
	return s.d.Entities
}

// kindOut is a builder.BootKind with the readable names an agent expects
// (the admin boot payload keeps its short ones, u and ia).
type kindOut struct {
	Kind        string             `json:"kind"`
	Children    bool               `json:"children"`
	Props       []builder.BootProp `json:"props"`
	Default     map[string]any     `json:"default,omitempty"`
	Label       string             `json:"label,omitempty"`
	Hint        string             `json:"hint,omitempty"`
	Group       string             `json:"group,omitempty"`
	Seed        []builder.SeedNode `json:"seed,omitempty"`
	Hidden      bool               `json:"hidden,omitempty"`
	Display     string             `json:"display,omitempty"`
	Universal   []string           `json:"universal,omitempty"`
	Interactive bool               `json:"interactive,omitempty"`
	OnlyIn      string             `json:"onlyIn,omitempty"`
}

func kindsOut() []kindOut {
	boot := builder.BootKinds(true)
	out := make([]kindOut, len(boot))
	for i, k := range boot {
		out[i] = kindOut{k.Kind, k.Children, k.Props, k.Default, k.Label, k.Hint, k.Group, k.Seed, k.Hidden, k.Display, k.Universal, k.Interactive, k.OnlyIn}
	}
	return out
}

func (s *Server) registerBuilder() {
	type kindsIn struct {
		Kind string `json:"kind,omitempty" jsonschema:"only this node kind (for example container)"`
	}
	register(s, toolSpec{name: "builder_kinds", description: "List the page builder's node kinds: props with types and limits, whether the kind takes children, the one parent kind it must sit in (onlyIn), whether it is interactive (not allowed inside a linked container), and which universal props it takes (universal; their limits are in universalProps). Read this before building a page with builder_apply_ops.", scope: readScope, idempotent: true},
		func(_ context.Context, _ *hmauth.APIKey, in kindsIn) (any, error) {
			all := kindsOut()
			if in.Kind == "" {
				return object{"kinds": all, "universalProps": builder.UniversalProps(), "bindSources": bindSources()}, nil
			}
			for _, k := range all {
				if k.Kind == in.Kind {
					return object{"kinds": []kindOut{k}, "universalProps": builder.UniversalProps()}, nil
				}
			}
			return nil, userErr("unknown kind %q", in.Kind)
		})

	type noIn struct{}
	register(s, toolSpec{name: "builder_stylespec", description: "The style vocabulary of a node's s map: every style key with its value rules, plus the breakpoints and states a style map key (bp or bp:state) is made of.", scope: readScope, idempotent: true},
		func(context.Context, *hmauth.APIKey, noIn) (any, error) {
			return object{"keys": builder.StyleSpecTable(), "breakpoints": builder.Breakpoints(), "states": builder.States()}, nil
		})

	register(s, toolSpec{name: "builder_design", description: "The site's design tokens and presets, read only: tokens are the names a style value takes (space-4, primary, lg), the palette (colour names, spacing steps and font sizes with their resolved values, usable as style values) and the named style presets.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, _ noIn) (any, error) {
			di := defaultDesign()
			if s.d.Design != nil {
				var err error
				if di, err = s.d.Design(ctx); err != nil {
					return nil, err
				}
			}
			if di.Presets == nil {
				di.Presets = []DesignPreset{}
			}
			return object{
				"tokens":  object{"color": sortedKeys(di.Colors), "space": spaceTokenNames(di.Space), "fs": sortedKeys(di.FS)},
				"palette": object{"colors": di.Colors, "space": di.Space, "fs": di.FS},
				"presets": di.Presets,
			}, nil
		})

	s.registerBuilderDocs()
	s.registerBuilderRender()
}

func bindSources() []object {
	out := []object{}
	for _, b := range builder.BindSources() {
		out = append(out, object{"source": b.Source, "label": b.Label, "group": b.Group, "class": b.Class})
	}
	return out
}

// defaultDesign is the design of a site without its own tokens.
func defaultDesign() DesignInfo {
	t := builder.DefaultTokens()
	return DesignInfo{Colors: t.Color, Space: t.Space, FS: t.FS}
}

// spaceTokenNames are the spacing names a style value takes ("space-4"), in step order.
func spaceTokenNames(space map[string]string) []string {
	steps := make([]int, 0, len(space))
	for k := range space {
		if n, err := strconv.Atoi(k); err == nil {
			steps = append(steps, n)
		}
	}
	sort.Ints(steps)
	out := make([]string, len(steps))
	for i, n := range steps {
		out[i] = "space-" + strconv.Itoa(n)
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
