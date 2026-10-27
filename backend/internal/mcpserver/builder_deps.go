package mcpserver

import (
	"context"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/sectiontemplate"
)

// DesignInfo is the site's resolved design: the palette the Style tab shows
// and the named style presets.
type DesignInfo struct {
	// Colors by name (primary bg text surface muted accent), Space by step
	// ("0".."12") and FS by name (xs..4xl); the values style keys accept as tokens.
	Colors, Space, FS map[string]string
	Presets           []DesignPreset
}

// DesignPreset is a named bundle of style entries a node can apply.
type DesignPreset struct {
	Name  string                       `json:"name"`
	Label string                       `json:"label"`
	S     map[string]map[string]string `json:"s"`
}

// TemplateStore is the section template service as the builder tools use it.
type TemplateStore interface {
	List(ctx context.Context, f *sectiontemplate.ListFilter, limit, offset int) (*sectiontemplate.ListResult, error)
	Get(ctx context.Context, id uuid.UUID) (*sectiontemplate.SectionTemplate, error)
}

// RenderRequest asks for the page rendered with Doc as its page_doc (nothing
// is stored). IDs, when set, limit the output to those nodes.
type RenderRequest struct {
	Entity *entity.Entity
	Doc    []byte
	IDs    []string
}

// RenderResult is the rendered markup and the builder CSS it needs.
type RenderResult struct {
	HTML, CSS string
}

// builderStore is what the builder tools need of the entity service
// (entity.BuilderStore, which *entity.Service satisfies).
type builderStore = entity.BuilderStore
