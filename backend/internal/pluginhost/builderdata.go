package pluginhost

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// ExtSources adapts the builder.data hook chain to render.ExtSourceFunc: one
// Apply per batch. Each plugin sees only the requests that name its own
// declared sources, and only results for those requests are taken back.
// Nil engine gives nil (no external data).
func ExtSources(h *hooks.Engine) render.ExtSourceFunc {
	if h == nil {
		return nil
	}
	return func(ctx context.Context, b render.ExtBatch) map[string]render.ExtResult {
		if len(b.Requests) == 0 || !h.HasFilter(sdk.BuilderData.Name) {
			return nil
		}
		in := sdk.BuilderDataRequest{Path: b.Path, Language: b.Language, SchemaSlug: b.SchemaSlug, EntityID: b.EntityID}
		for _, q := range b.Requests {
			if len(in.Requests) == sdk.MaxBuilderDataRequests {
				render.NoteDegradedCtx(ctx)
				break
			}
			in.Requests = append(in.Requests, sdk.BuilderDataQuery{ID: q.ID, Source: q.Source, Limit: clampItems(q.Limit)})
		}
		out, ok := h.Apply(ctx, sdk.BuilderData.Name, in).(sdk.BuilderDataRequest)
		if !ok {
			return nil
		}
		res := make(map[string]render.ExtResult, len(out.Results))
		for _, r := range out.Results {
			if _, dup := res[r.ID]; dup {
				continue
			}
			er := render.ExtResult{Value: r.Value, OK: r.Error == ""}
			for _, it := range r.Items {
				er.Items = append(er.Items, relatedOf(it))
			}
			res[r.ID] = er
		}
		return res
	}
}

func clampItems(n int) int {
	if n <= 0 || n > sdk.MaxBuilderDataItems {
		return sdk.MaxBuilderDataItems
	}
	return n
}

// builderDataFilter is the filter of one plugin: it hands the plugin its own
// requests, validates the answer against the declared sources and the caps,
// and appends the valid results to the chain's value.
func (r *Runtime) builderDataFilter(m sdk.Manifest, export string) func(context.Context, any) (any, error) {
	generic := r.filter(m.Name, export)
	declared := map[string]string{} // full source id -> kind
	for _, s := range m.BuilderSources {
		declared["ext."+m.Name+"."+s.Name] = s.Kind
	}
	return noteRenderFailure(sdk.BuilderData.Name, func(ctx context.Context, v any) (any, error) {
		cur, ok := v.(sdk.BuilderDataRequest)
		if !ok || !r.caps.grants.Has(m.Name, sdk.PermBuilderData) {
			return v, nil
		}
		in := cur
		in.Requests, in.Results = nil, nil
		kept := map[string]string{} // request id -> source kind
		for _, q := range cur.Requests {
			if kind, own := declared[q.Source]; own {
				in.Requests = append(in.Requests, q)
				kept[q.ID] = kind
			}
		}
		if len(in.Requests) == 0 {
			return v, nil // nothing for this plugin: no call
		}
		out, err := generic(ctx, in)
		if err != nil {
			return v, err
		}
		res, ok := out.(sdk.BuilderDataRequest)
		if !ok {
			return v, nil
		}
		next := cur
		next.Results = slices.Clone(cur.Results)
		seen := map[string]bool{}
		for _, br := range res.Results {
			kind, own := kept[br.ID]
			if !own || seen[br.ID] {
				continue // not asked of this plugin: ignored
			}
			seen[br.ID] = true
			if err := checkBuilderResult(kind, br); err != nil {
				r.log.Warn("plugin builder.data result dropped", "plugin", m.Name, "id", br.ID, "error", err)
				render.NoteDegradedCtx(ctx)
				br = sdk.BuilderDataResult{ID: br.ID, Error: "dropped"}
			}
			next.Results = append(next.Results, br)
		}
		return next, nil
	})
}

// checkBuilderResult enforces the caps of the contract on one result; a
// violation drops the whole result rather than a partial list.
func checkBuilderResult(kind string, r sdk.BuilderDataResult) error {
	if r.Error != "" {
		return nil
	}
	if !validBuilderString(r.Value) {
		return fmt.Errorf("value over %d bytes or not UTF-8", sdk.MaxBuilderDataString)
	}
	switch {
	case kind == sdk.BuilderSourceValue && len(r.Items) > 0:
		return fmt.Errorf("items from a value source")
	case kind == sdk.BuilderSourceItems && r.Value != "":
		return fmt.Errorf("value from an items source")
	case len(r.Items) > sdk.MaxBuilderDataItems:
		return fmt.Errorf("%d items, at most %d", len(r.Items), sdk.MaxBuilderDataItems)
	}
	for _, it := range r.Items {
		for _, s := range []string{it.Title, it.URL, it.Excerpt, it.Image, it.Category, it.Author, it.AuthorURL, it.PublishedAt} {
			if !validBuilderString(s) {
				return fmt.Errorf("item string over %d bytes or not UTF-8", sdk.MaxBuilderDataString)
			}
		}
		if _, err := parseItemTime(it.PublishedAt); err != nil {
			return fmt.Errorf("published_at: %w", err)
		}
		if len(it.Attrs) > sdk.MaxBuilderDataAttrs {
			return fmt.Errorf("more than %d attrs", sdk.MaxBuilderDataAttrs)
		}
		for k, v := range it.Attrs {
			if k == "" || !validBuilderString(k) || !validBuilderString(v) {
				return fmt.Errorf("attr %q: bad key or value over %d bytes", k, sdk.MaxBuilderDataString)
			}
		}
	}
	return nil
}

func validBuilderString(s string) bool {
	return len(s) <= sdk.MaxBuilderDataString && utf8.ValidString(s)
}

func parseItemTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

func relatedOf(it sdk.BuilderDataItem) render.RelatedItem {
	t, _ := parseItemTime(it.PublishedAt) // checked by checkBuilderResult
	ri := render.RelatedItem{
		Title: it.Title, Name: it.Title, URL: it.URL, Excerpt: it.Excerpt, Image: it.Image, Category: it.Category,
		Author: it.Author, AuthorURL: it.AuthorURL, PublishedAt: t,
	}
	if len(it.Attrs) > 0 {
		ri.Attrs = make(map[string]any, len(it.Attrs))
		for k, v := range it.Attrs {
			ri.Attrs[k] = v
		}
	}
	return ri
}

// syncExtSources publishes the granted sources to the builder vocabulary
// (palette, validation, render requests). Called whenever a plugin loads,
// unloads or its grants change.
func (r *Runtime) syncExtSources() { builder.SetExtSources(r.ExtSourceList()) }

// ExtSourceList returns the builder data sources of every loaded plugin that
// holds the builder.data grant, for the editor vocabulary.
func (r *Runtime) ExtSourceList() []builder.ExtSource {
	var out []builder.ExtSource
	for _, st := range r.Statuses() {
		if !st.Loaded || !r.caps.grants.Has(st.Name, sdk.PermBuilderData) {
			continue
		}
		for _, s := range st.Manifest.BuilderSources {
			out = append(out, builder.ExtSource{Plugin: st.Name, Name: s.Name, Label: s.Label, Kind: s.Kind, Class: s.Class})
		}
	}
	slices.SortFunc(out, func(a, b builder.ExtSource) int {
		if a.Plugin != b.Plugin {
			return strings.Compare(a.Plugin, b.Plugin)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}
