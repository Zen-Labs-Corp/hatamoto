package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	hmauth "hatamoto.dev/core/internal/auth"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/sectiontemplate"
)

const (
	// maxRenderBytes caps the markup and CSS one builder_render call returns.
	maxRenderBytes = 2 << 20
	maxRenderIDs   = 50
	// treeTemplateType is the section type tree templates are stored under.
	treeTemplateType = "hm-tree"
	templateListMax  = 100
)

// ErrRenderUnavailable is what Deps.RenderPage returns when the site has no
// render engine (page rendering is off).
var ErrRenderUnavailable = errors.New("page rendering is not available on this site")

func (s *Server) registerBuilderRender() {
	type renderIn struct {
		EntityID string   `json:"entity_id" jsonschema:"the page to render"`
		IDs      []string `json:"ids,omitempty" jsonschema:"render only these nodes (at most 50); omitted renders the whole page"`
		Ops      []opIn   `json:"ops,omitempty" jsonschema:"ops to try first, as in builder_apply_ops; nothing is stored"`
	}
	register(s, toolSpec{name: "builder_render", description: "Render a page's layout, or only some of its nodes, without storing anything; ops are tried first as a dry run. Returns {html, css}. A whole page is a full HTML document (css is its builder stylesheet, already inside it); with ids you get the nodes' markup as the builder canvas draws it and their CSS. Output over 2 MiB is refused.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, in renderIn) (any, error) {
			if s.d.RenderPage == nil {
				return nil, codedErr(codeUnavailable, "%s", ErrRenderUnavailable)
			}
			if len(in.IDs) > maxRenderIDs {
				return nil, codedErr(codeLimit, "at most %d ids per call", maxRenderIDs)
			}
			t, err := s.loadTarget(ctx, in.EntityID)
			if err != nil {
				return nil, err
			}
			d, err := t.copyDoc()
			if err != nil {
				return nil, codedErr(codeNotBuilder, "%v", err)
			}
			ids := []genID{}
			if len(in.Ops) > 0 {
				if err := checkOpLimits(in.Ops); err != nil {
					return nil, err
				}
				if ids, err = applyOps(d, in.Ops); err != nil {
					return nil, codedErr(codeInvalidPageDoc, "%v", err)
				}
				if ids == nil {
					ids = []genID{}
				}
			}
			for _, id := range in.IDs {
				if n, _ := d.Find(id); n == nil {
					return nil, codedErr(codeInvalidPageDoc, "no such node %q", id)
				}
			}
			raw, err := json.Marshal(d)
			if err != nil {
				return nil, err
			}
			res, err := s.d.RenderPage(ctx, RenderRequest{Entity: t.e, Doc: raw, IDs: in.IDs})
			switch {
			case errors.Is(err, ErrRenderUnavailable):
				return nil, codedErr(codeUnavailable, "%s", err)
			case err != nil:
				return nil, err
			}
			if len(res.HTML)+len(res.CSS) > maxRenderBytes {
				return nil, codedErr(codeLimit, "render too large (over %d bytes)", maxRenderBytes)
			}
			return object{"html": res.HTML, "css": res.CSS, "ids": ids}, nil
		})

	type noIn struct{}
	type templateItem struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Kind        string `json:"kind"`
	}
	register(s, toolSpec{name: "builder_templates_list", description: "List the saved section templates that hold builder nodes (tree templates), up to 100. Insert one with builder_template_nodes, then builder_apply_ops. Older section templates need the theme's section types and are not listed.", scope: readScope, idempotent: true},
		func(ctx context.Context, _ *hmauth.APIKey, _ noIn) (any, error) {
			items := []templateItem{}
			if s.d.Templates == nil {
				return object{"templates": items}, nil
			}
			res, err := s.d.Templates.List(ctx, &sectiontemplate.ListFilter{SectionType: treeTemplateType}, templateListMax, 0)
			if err != nil {
				return nil, err
			}
			if res != nil {
				for _, t := range res.Items {
					if t.SectionType == treeTemplateType {
						items = append(items, templateItem{ID: t.ID.String(), Name: t.Name, Description: t.Description, Kind: "tree"})
					}
				}
			}
			return object{"templates": items}, nil
		})

	type tplIn struct {
		ID string `json:"id" jsonschema:"template id from builder_templates_list"`
	}
	register(s, toolSpec{name: "builder_template_nodes", description: "The nodes of a tree template with fresh ids on every call, ready to use as the node of an insert op in builder_apply_ops (one insert per top-level node).", scope: readScope},
		func(ctx context.Context, _ *hmauth.APIKey, in tplIn) (any, error) {
			if s.d.Templates == nil {
				return nil, codedErr(codeUnavailable, "section templates are not available on this site")
			}
			id, err := parseID("id", in.ID)
			if err != nil {
				return nil, err
			}
			tpl, err := s.d.Templates.Get(ctx, id)
			if errors.Is(err, sectiontemplate.ErrTemplateNotFound) {
				return nil, userErr("no such template")
			}
			if err != nil {
				return nil, err
			}
			if tpl.SectionType != treeTemplateType {
				return nil, userErr("only tree templates can be inserted through MCP")
			}
			nodes := builder.ReadableNodes(tpl.Content)
			if len(nodes) == 0 {
				return nil, userErr("this template has nothing this version can insert")
			}
			return object{"id": tpl.ID, "name": tpl.Name, "kind": "tree", "nodes": builder.FreshIDs(nodes, newNodeID)}, nil
		})
}

// JoinCSS renders a node CSS map (id -> rules) as one stylesheet, in key order.
func JoinCSS(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(m[k])
		b.WriteByte('\n')
	}
	return b.String()
}
