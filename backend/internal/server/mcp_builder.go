package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/mcpserver"
	"hatamoto.dev/core/internal/siteconfig"
)

// The wiring of the MCP builder tools to the site's own services.

// mcpDesign is builder_design: the palette resolved from the site config (the
// defaults when it cannot be read) and the site's named presets.
func (s *Server) mcpDesign(ctx context.Context) (mcpserver.DesignInfo, error) {
	var cfg *siteconfig.Config
	if s.siteConfig != nil {
		if c, err := s.siteConfig.Get(ctx); err == nil {
			cfg = c
		}
	}
	var di mcpserver.DesignInfo
	di.Colors, di.Space, di.FS = admin.BuilderPalette(cfg)
	if cfg != nil {
		design, _ := builder.ParseDesign(cfg.ThemeExtra)
		names := make([]string, 0, len(design.Presets))
		for n := range design.Presets {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			p := design.Presets[n]
			di.Presets = append(di.Presets, mcpserver.DesignPreset{Name: n, Label: p.Label, S: p.S})
		}
	}
	return di, nil
}

// mcpRenderPage is builder_render: the page (or some of its nodes) rendered
// with req.Doc as its layout, by the engine paths the admin preview and the
// canvas use, through a synthetic request. Nothing is stored or cached.
func (s *Server) mcpRenderPage(ctx context.Context, req mcpserver.RenderRequest) (mcpserver.RenderResult, error) {
	if s.renderEngine == nil {
		return mcpserver.RenderResult{}, mcpserver.ErrRenderUnavailable
	}
	e := req.Entity
	attrs := map[string]json.RawMessage{}
	_ = json.Unmarshal(e.Attributes, &attrs)
	delete(attrs, "page_sections")
	attrs["page_doc"] = req.Doc
	body := map[string]any{"entity_id": e.ID.String(), "attributes": attrs, "name": e.Name, "updated_at": e.UpdatedAt}
	if e.Schema != nil {
		body["schema"] = e.Schema.Slug
	}
	if e.PublishedAt != nil {
		body["published_at"] = e.PublishedAt
	}
	if e.Status == entity.StatusPublished {
		if p := s.entityPublicPath(ctx, e); p != "" {
			body["path"] = p
		}
	}
	h := s.newRenderAdminHandlers(s.renderEngine)
	if len(req.IDs) == 0 {
		body["canvas"] = false
		rec, err := s.serveRender(ctx, h.handleRenderPreview, body)
		if err != nil {
			return mcpserver.RenderResult{}, err
		}
		page := rec.body.String()
		return mcpserver.RenderResult{HTML: page, CSS: builderStylesheet(page)}, nil
	}
	body["doc"] = json.RawMessage(req.Doc)
	body["render"] = req.IDs
	body["css"] = req.IDs
	rec, err := s.serveRender(ctx, h.handleRenderNodes, body)
	if err != nil {
		return mcpserver.RenderResult{}, err
	}
	var out renderNodesResponse
	if err := json.Unmarshal(rec.body.Bytes(), &out); err != nil {
		return mcpserver.RenderResult{}, errors.New("render failed")
	}
	var html strings.Builder
	for _, f := range out.Fragments {
		html.WriteString(f.HTML)
		html.WriteByte('\n')
		for k, v := range f.CSS {
			out.CSS[k] = v
		}
	}
	return mcpserver.RenderResult{HTML: html.String(), CSS: mcpserver.JoinCSS(out.CSS)}, nil
}

// serveRender posts body to a render handler in process and returns the 200 answer.
func (s *Server) serveRender(ctx context.Context, h http.HandlerFunc, body map[string]any) (*memResponse, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	host := "localhost"
	if s.cfg != nil {
		if u, err := url.Parse(strings.TrimSpace(s.cfg.PublicSite.BaseURL)); err == nil && u.Host != "" {
			host = u.Host
		}
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+host+"/render", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "hatamoto-mcp-render")
	rec := &memResponse{header: http.Header{}}
	h(rec, r)
	if rec.status != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.body.Bytes(), &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(rec.body.String())
		}
		return nil, errors.New("render refused: " + e.Error)
	}
	if rec.body.Len() >= maxMCPBody {
		return nil, errors.New("render too large")
	}
	return rec, nil
}

// builderStylesheet is the content of the page's builder <style> block.
func builderStylesheet(page string) string {
	const open = `<style id="hm-builder-css">`
	i := strings.Index(page, open)
	if i < 0 {
		return ""
	}
	rest := page[i+len(open):]
	if j := strings.Index(rest, "</style>"); j >= 0 {
		return rest[:j]
	}
	return ""
}
