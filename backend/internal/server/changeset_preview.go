package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/render"
)

// PreviewChangeset renders one page of a staged changeset as the site would
// show it once the changeset is published: the HTML document of the entity
// item seq (the first entity item when seq names none). Everything is read; the
// changeset, the live content and the source-key table are never written.
// A changeset that is not staged answers changeset.ErrNotStaged, an unknown one
// changeset.ErrNotFound. An item the preview cannot show answers a neutral
// notice document, never an error.
func (s *Server) PreviewChangeset(ctx context.Context, id int64, seq int) ([]byte, error) {
	if s.changesets == nil || s.renderEngine == nil {
		return nil, errors.New("changeset preview is not available")
	}
	h, err := s.changesets.Header(ctx, id)
	if err != nil {
		return nil, err
	}
	if h.State != changeset.StateStaged {
		return nil, changeset.ErrNotStaged
	}
	if !h.ExpiresAt.IsZero() && !h.ExpiresAt.After(time.Now()) {
		return nil, changeset.ErrExpired
	}
	o, err := s.stagedSets.get(h, func() (*stagedChangeset, error) {
		_, items, err := s.changesets.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		return newStagedChangeset(ctx, h, items, s.stagedSource(ctx)), nil
	})
	if err != nil {
		return nil, err
	}
	if r := o.redirectBySeq(seq); r != nil {
		return redirectNotice(r.Source, r.Target, r.Status), nil
	}
	se := o.pick(seq)
	if se == nil {
		if seq > 0 {
			return previewNotice("This item cannot be previewed."), nil
		}
		return previewNotice("Nothing to preview in this changeset."), nil
	}
	body, err := s.renderStaged(withStaged(withHiddenLocalesServed(ctx), o), o, se)
	if err != nil {
		s.logger.Warn("changeset preview: item not shown", "changeset", id, "seq", se.Seq, "error", err)
		return previewNotice("This item cannot be previewed."), nil
	}
	return body, nil
}

// pick is the staged entity of item seq. Only a request that names no item
// (seq 0) gets the first one: an item the staged set dropped must never show
// another page in its place.
func (o *stagedChangeset) pick(seq int) *stagedEntity {
	for _, se := range o.items {
		if se.Seq == seq {
			return se
		}
	}
	if seq <= 0 && len(o.items) > 0 {
		return o.items[0]
	}
	return nil
}

func (s *Server) stagedSource(ctx context.Context) stagedSource {
	repo := entity.NewPostgresPublicRepository(s.db)
	return stagedSource{
		snapshot: s.urlSnapshot(ctx),
		entity:   s.entityService.GetEntity,
		schema:   s.entityService.GetSchemaBySlug,
		menuBySlug: func(ctx context.Context, slug string) (uuid.UUID, bool) {
			if s.menuService == nil {
				return uuid.Nil, false
			}
			m, err := s.menuService.GetMenuBySlug(ctx, slug)
			if err != nil || m == nil {
				return uuid.Nil, false
			}
			return m.ID, true
		},
		mapped: func(ctx context.Context, origin string, kind changeset.Kind, sourceKey string) (uuid.UUID, bool, error) {
			conn, err := s.db.GetConn(ctx)
			if err != nil {
				return uuid.Nil, false, err
			}
			defer conn.Release()
			var target string
			err = conn.QueryRow(ctx, `SELECT target_id FROM hatamoto_changeset_source_keys WHERE origin = $1 AND kind = $2 AND source_key = $3`,
				origin, string(kind), sourceKey).Scan(&target)
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.Nil, false, nil
			}
			if err != nil {
				return uuid.Nil, false, err
			}
			id, err := uuid.Parse(target)
			return id, err == nil, err
		},
		mappedMany: func(ctx context.Context, origin string, kind changeset.Kind, keys []string) (map[string]uuid.UUID, error) {
			rows, err := s.db.Pool().Query(ctx, `SELECT source_key, target_id FROM hatamoto_changeset_source_keys WHERE origin = $1 AND kind = $2 AND source_key = ANY($3)`,
				origin, string(kind), keys)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			out := make(map[string]uuid.UUID, len(keys))
			for rows.Next() {
				var key, target string
				if err := rows.Scan(&key, &target); err != nil {
					return nil, err
				}
				if id, err := uuid.Parse(target); err == nil {
					out[key] = id
				}
			}
			return out, rows.Err()
		},
		ancestors: func(ctx context.Context, parent uuid.UUID) []string {
			path, err := repo.GetEntityPath(ctx, parent)
			if err != nil {
				return nil
			}
			out := make([]string, 0, len(path))
			for _, p := range path {
				out = append(out, p.Slug)
			}
			return out
		},
	}
}

// stagedRenderOpts varies renderStagedWith. The zero value is a changeset
// preview.
type stagedRenderOpts struct {
	// draftBanner renders a shared draft preview: the "Preview of a draft"
	// banner and no change marks.
	draftBanner bool
	// from is the request being answered. Its host, scheme and proxy headers
	// give the rendered page an absolute canonical address.
	from *http.Request
}

// renderStaged renders se through the public route, with the staged set on ctx.
func (s *Server) renderStaged(ctx context.Context, o *stagedChangeset, se *stagedEntity) ([]byte, error) {
	return s.renderStagedWith(ctx, o, se, stagedRenderOpts{})
}

// renderStagedWith is renderStaged with options.
func (s *Server) renderStagedWith(ctx context.Context, o *stagedChangeset, se *stagedEntity, opts stagedRenderOpts) ([]byte, error) {
	if se.Path == "" {
		return nil, errors.New("the schema has no public route")
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, se.Path, nil)
	if err != nil {
		return nil, err
	}
	if opts.from != nil {
		r.Host, r.TLS, r.RemoteAddr = opts.from.Host, opts.from.TLS, opts.from.RemoteAddr
		for _, k := range []string{"X-Forwarded-Proto", "X-Forwarded-Host"} {
			if v := opts.from.Header.Get(k); v != "" {
				r.Header.Set(k, v)
			}
		}
	}
	// Only staged redirects are consulted: a live rule must not hide the staged
	// page, and a sandboxed frame never follows a Location.
	deps := routeDeps{
		checkRedirect: func(_ context.Context, p string) *redirect.Redirect { return o.matchRedirect(p) },
		snapshot:      s.urlSnapshotOrErr,
		repo:          entity.NewPostgresPublicRepository(s.db),
		loadPage:      s.loadPublicPage,
		loadHome:      s.loadHomepage,
	}
	rr, _, err := s.resolveRouteWith(ctx, deps, se.Path, r)
	if err != nil {
		return nil, err
	}
	if rr != nil && rr.Redirect != nil {
		return redirectNotice(se.Path, rr.Redirect.Target, rr.Redirect.Status), nil
	}
	if rr == nil {
		return nil, errors.New("the path does not resolve to a page")
	}
	if !se.Delete && (rr.NotFound || routeEntityID(rr) != se.Ent.ID) {
		return nil, errors.New("the path does not resolve to the staged page")
	}
	v, err := s.buildPageView(ctx, r, rr)
	if err != nil {
		return nil, err
	}
	sanitizeView(v)
	v.Preview = true
	v.Page.NoIndex = true
	if opts.draftBanner {
		v.DraftBanner = true
	} else {
		v.Changes = o.changesFor(se)
	}
	var buf bytes.Buffer
	if err := s.renderEngine.RenderPage(&buf, v); err != nil {
		return nil, err
	}
	return o.maskMedia(buf.Bytes()), nil
}

// changesFor marks what the item changes: a created page is new as a whole; an
// update marks the builder nodes that were added or changed, or the whole page
// when no node differs (title, SEO and other attributes). A delete shows the
// site's not-found page unmarked.
func (o *stagedChangeset) changesFor(se *stagedEntity) *render.PageChanges {
	c := &render.PageChanges{Label: "#" + strconv.FormatInt(o.header.ID, 10)}
	switch {
	case se.Delete:
		return c
	case se.Live == nil:
		c.Page = "new"
		return c
	}
	id := se.Ent.ID.String()
	diff := builder.DiffDocs(docFromAttributes(se.Live.Attributes, id), docFromAttributes(se.Ent.Attributes, id))
	for _, ch := range diff.Changes {
		switch ch.Op {
		case builder.DiffAdded:
			c = markNode(c, ch.ID, "new")
		case builder.DiffChanged, builder.DiffMoved:
			c = markNode(c, ch.ID, "changed")
		}
	}
	if len(c.Nodes) == 0 {
		c.Page = "changed"
	}
	return c
}

func markNode(c *render.PageChanges, id, kind string) *render.PageChanges {
	if c.Nodes == nil {
		c.Nodes = map[string]string{}
	}
	c.Nodes[id] = kind
	return c
}

// previewNotice is the neutral document shown for an item the preview cannot
// render. No script, no external reference.
func previewNotice(msg string) []byte {
	return []byte(fmt.Sprintf(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="robots" content="noindex"><title>Preview</title></head><body style="font:16px/1.5 system-ui,sans-serif;margin:3rem;color:#333"><p>%s</p></body></html>`, html.EscapeString(msg)))
}

const (
	stagedCacheTTL     = 30 * time.Second
	stagedCacheEntries = 4
)

// stagedCache keeps the few most recently built staged sets, so stepping
// through a changeset's items does not rebuild it (and re-read every payload)
// on each frame load. An entry is only used for the same changeset version
// (run, item count, expiry) and for a short time, since live rows can change.
// A staged set is read-only once built, so it is shared.
type stagedCache struct {
	mu      sync.Mutex
	entries []stagedCacheEntry
}

type stagedCacheEntry struct {
	h   changeset.Header
	at  time.Time
	set *stagedChangeset
}

func (c *stagedCache) get(h changeset.Header, build func() (*stagedChangeset, error)) (*stagedChangeset, error) {
	now := time.Now()
	c.mu.Lock()
	for _, e := range c.entries {
		if e.h.ID == h.ID && e.h.RunID == h.RunID && e.h.ItemCount == h.ItemCount && e.h.ExpiresAt.Equal(h.ExpiresAt) && now.Sub(e.at) < stagedCacheTTL {
			c.mu.Unlock()
			return e.set, nil
		}
	}
	c.mu.Unlock()
	set, err := build()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.entries[:0:0]
	for _, e := range c.entries {
		if e.h.ID != h.ID && now.Sub(e.at) < stagedCacheTTL {
			kept = append(kept, e)
		}
	}
	if len(kept) >= stagedCacheEntries {
		kept = kept[len(kept)-stagedCacheEntries+1:]
	}
	c.entries = append(kept, stagedCacheEntry{h: h, at: now, set: set})
	return set, nil
}
