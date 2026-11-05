package server

import (
	"bytes"
	"context"
	"html"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/redirect"
)

// stageOthers adds the menu, redirect and media items of a changeset to the
// staged set. It runs after the entity items so a menu can link them by source
// key. Deletes of these kinds are not shown (the live object stays visible);
// term items appear in the changeset's item list only.
func (o *stagedChangeset) stageOthers(ctx context.Context, src stagedSource, origin string, items []changeset.StoredItem) {
	for _, it := range items {
		if it.Op != changeset.OpUpsert {
			continue
		}
		switch it.Kind {
		case changeset.KindMenu:
			o.stageMenu(ctx, src, origin, it)
		case changeset.KindRedirect:
			var p struct {
				SourcePath string `json:"source_path"`
				Target     string `json:"target"`
				Status     int    `json:"status"`
			}
			if jsonx.Unmarshal(it.Payload, &p, jsonx.Stored) == nil && strings.HasPrefix(p.SourcePath, "/") {
				o.redirects = append(o.redirects, &stagedRedirect{Seq: it.Seq, Source: p.SourcePath, Target: p.Target, Status: p.Status})
			}
		case changeset.KindMedia:
			var p struct {
				Filename string `json:"filename"`
			}
			if jsonx.Unmarshal(it.Payload, &p, jsonx.Stored) == nil && p.Filename != "" {
				o.media[p.Filename] = true
			}
		}
	}
}

func (o *stagedChangeset) stageMenu(ctx context.Context, src stagedSource, origin string, it changeset.StoredItem) {
	var p struct {
		Slug       string             `json:"slug"`
		Items      []menu.ReplaceItem `json:"items"`
		EntityKeys map[int]string     `json:"entity_keys"`
	}
	if jsonx.Unmarshal(it.Payload, &p, jsonx.Stored) != nil {
		return
	}
	var id uuid.UUID
	switch {
	case it.TargetID != "":
		parsed, err := uuid.Parse(it.TargetID)
		if err != nil {
			return
		}
		id = parsed
	case it.SourceKey != "":
		if m, found, err := src.mapped(ctx, origin, changeset.KindMenu, it.SourceKey); err == nil && found {
			id = m
		}
	}
	if id == uuid.Nil && p.Slug != "" && src.menuBySlug != nil {
		id, _ = src.menuBySlug(ctx, p.Slug)
	}
	if id == uuid.Nil {
		return // a menu the publish would create has no location: it shows nowhere
	}
	// Entity links to an earlier run's entities resolve as the publish does.
	for _, key := range p.EntityKeys {
		if _, ok := o.keys[key]; ok {
			continue
		}
		if eid, found, err := src.mapped(ctx, origin, changeset.KindEntity, key); err == nil && found {
			o.keys[key] = eid
		}
	}
	o.menus[id] = p.Items
	o.menuLinks[id] = p.EntityKeys
}

// menuItems is the staged item tree of the live menu id, rebuilt on each call
// so a render may modify it. Entity links resolve through the staged entities
// first, then live ones (resolveMenuEntities).
func (o *stagedChangeset) menuItems(ctx context.Context, links *navLinks, id uuid.UUID) ([]*menu.MenuItem, bool) {
	flat, ok := o.menus[id]
	if !ok {
		return nil, false
	}
	nodes := make([]*menu.MenuItem, len(flat))
	var top []*menu.MenuItem
	for i, it := range flat {
		n := &menu.MenuItem{Label: it.Label, LinkType: it.LinkType, EntityID: it.EntityID, URL: it.URL, SchemaSlug: it.SchemaSlug,
			OpenInNewTab: it.OpenInNewTab, CSSClasses: it.CSSClasses, Attrs: it.Attrs}
		if key, ok := o.menuLinks[id][i]; ok {
			if eid, found := o.keys[key]; found {
				n.EntityID = &eid
			}
		}
		nodes[i] = n
		if it.Parent >= 0 && it.Parent < i {
			parent := nodes[it.Parent]
			n.Position = len(parent.Children)
			parent.Children = append(parent.Children, n)
			continue
		}
		n.Position = len(top)
		top = append(top, n)
	}
	resolveMenuEntities(ctx, links, top)
	o.fillStagedTargets(top)
	return top, true
}

// fillStagedTargets points links at staged entities by their staged slug.
func (o *stagedChangeset) fillStagedTargets(items []*menu.MenuItem) {
	for _, n := range items {
		if n.EntityID != nil {
			if se, ok := o.ents[*n.EntityID]; ok && se.Ent.Schema != nil {
				slug, schema := se.Ent.Slug, se.Ent.Schema.Slug
				n.EntitySlug, n.EntitySchemaSlug = &slug, &schema
			}
		}
		o.fillStagedTargets(n.Children)
	}
}

// matchRedirect answers a staged redirect for p. Live rules are deliberately
// not consulted: a live redirect must not hide the page being previewed.
func (o *stagedChangeset) matchRedirect(p string) *redirect.Redirect {
	for _, r := range o.redirects {
		if r.Source == p {
			return &redirect.Redirect{SourcePath: r.Source, TargetURL: r.Target, StatusCode: r.Status, IsActive: true}
		}
	}
	return nil
}

// redirectBySeq is the staged redirect of item seq.
func (o *stagedChangeset) redirectBySeq(seq int) *stagedRedirect {
	for _, r := range o.redirects {
		if r.Seq == seq {
			return r
		}
	}
	return nil
}

// redirectNotice is the in-frame answer for a redirect: a sandboxed frame
// never follows a Location, so the rule is described instead.
func redirectNotice(source, target string, status int) []byte {
	msg := "Redirects to " + target
	if status > 0 {
		msg += " (" + strconv.Itoa(status) + ")"
	}
	if source != "" {
		msg = source + ": " + msg
	}
	return previewNotice(msg)
}

// mediaPlaceholder is a neutral inline image (a grey box).
const mediaPlaceholder = `data:image/svg+xml,%3Csvg xmlns=%27http://www.w3.org/2000/svg%27 width=%27640%27 height=%27360%27%3E%3Crect width=%27100%25%27 height=%27100%25%27 fill=%27%23d1d5db%27/%3E%3C/svg%3E`

var srcAttrRe = regexp.MustCompile(`\bsrc="([^"]*)"`)

// maskMedia replaces references to staged media files with the placeholder.
// The staged files are not served, so the reference would be a broken image.
func (o *stagedChangeset) maskMedia(body []byte) []byte {
	if len(o.media) == 0 {
		return body
	}
	return srcAttrRe.ReplaceAllFunc(body, func(m []byte) []byte {
		ref := html.UnescapeString(string(bytes.TrimSuffix(m[len(`src="`):], []byte(`"`))))
		if u, err := url.Parse(ref); err == nil {
			ref = u.Path
		}
		if !o.media[path.Base(ref)] {
			return m
		}
		return []byte(`src="` + mediaPlaceholder + `"`)
	})
}
