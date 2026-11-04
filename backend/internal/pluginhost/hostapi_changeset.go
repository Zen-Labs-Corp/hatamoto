package pluginhost

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/taxonomy"
	"hatamoto.dev/sdk"
)

// The changeset.* functions stage writes into the changeset the run opened
// when it started. Nothing they do is live: the owner publishes the
// changeset. The plugin never names an origin, target or base token; the
// engine decides ownership from the source key, so an object the owner made
// or took over answers not_owned.
func init() {
	hostFns[sdk.FnChangesetEntityUpsert] = hostFn{run: fnChangesetEntityUpsert}
	hostFns[sdk.FnChangesetTermUpsert] = hostFn{run: fnChangesetTermUpsert}
	hostFns[sdk.FnChangesetPersonUpsert] = hostFn{run: fnChangesetPersonUpsert}
	hostFns[sdk.FnChangesetMediaAttach] = hostFn{run: fnChangesetMediaAttach}
	hostFns[sdk.FnChangesetMenuUpsert] = hostFn{run: fnChangesetMenuUpsert}
	hostFns[sdk.FnChangesetRedirectUpsert] = hostFn{run: fnChangesetRedirectUpsert}
	hostFns[sdk.FnChangesetDelete] = hostFn{run: fnChangesetDelete}
	hostFns[sdk.FnChangesetPropose] = hostFn{run: fnChangesetPropose}
}

// Limits of the changeset functions.
const (
	maxSourceKeyLen = 256
	maxNameLen      = 500
	maxSlugLen      = 200
	maxLocaleLen    = 35
	maxPathLen      = 2048
	maxMenuItems    = 1000
	maxMenuDepth    = 8
	maxLabelLen     = 200
)

// stager is a run's staging target.
type stager struct {
	a      *API
	rc     RunCtx
	csID   int64
	plugin string
}

// stager returns the run's changeset. Without the services, or before the
// run has a changeset, the functions are unavailable.
func (a *API) stager(ctx context.Context, plugin string) (*stager, error) {
	rc, runs, err := a.runStore(ctx)
	if err != nil {
		return nil, err
	}
	if a.svc.Changesets == nil {
		return nil, unavailable("changesets")
	}
	run, err := runs.Get(ctx, rc.RunID)
	if err != nil {
		return nil, runErr(err)
	}
	if run.ChangesetID == nil {
		return nil, herr(sdk.ErrUnavailable, "the run has no changeset")
	}
	return &stager{a: a, rc: rc, csID: *run.ChangesetID, plugin: plugin}, nil
}

// stage stages it and reports whether the source key was new to this plugin.
func (s *stager) stage(ctx context.Context, it changeset.Item) (sdk.UpsertResult, error) {
	_, mapped, err := s.a.svc.Changesets.Resolve(ctx, s.plugin, it.Kind, it.SourceKey)
	if err != nil {
		return sdk.UpsertResult{}, err
	}
	if err := s.a.svc.Changesets.Stage(ctx, s.csID, it); err != nil {
		return sdk.UpsertResult{}, err
	}
	return sdk.UpsertResult{SourceKey: it.SourceKey, Created: !mapped}, nil
}

// known reports whether an entity source key is one the owner of this plugin
// published before or one staged earlier in this changeset.
func (s *stager) known(ctx context.Context, kind changeset.Kind, key string) (bool, error) {
	if _, ok, err := s.a.svc.Changesets.Resolve(ctx, s.plugin, kind, key); err != nil || ok {
		return ok, err
	}
	return s.a.svc.Changesets.HasStaged(ctx, s.csID, kind, key)
}

func invalid(format string, v ...any) error { return herr(sdk.ErrInvalidArgument, format, v...) }

// text checks a required or optional single-line string.
func text(field, v string, max int, required bool) error {
	switch {
	case required && strings.TrimSpace(v) == "":
		return invalid("%s is required", field)
	case len(v) > max:
		return invalid("%s is over %d bytes", field, max)
	case !utf8.ValidString(v) || strings.ContainsAny(v, "\x00\r\n"):
		return invalid("%s has invalid characters", field)
	}
	return nil
}

func sourceKey(v string) error { return text("source_key", v, maxSourceKeyLen, true) }

func payload(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func fnChangesetEntityUpsert(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	in, err := decodeArgs[sdk.EntityUpsert](raw)
	if err != nil {
		return nil, err
	}
	for _, c := range []error{
		sourceKey(in.SourceKey),
		text("schema", in.Schema, maxSlugLen, true),
		text("name", in.Name, maxNameLen, true),
		text("slug", in.Slug, maxSlugLen, false),
		text("parent_key", in.ParentKey, maxSourceKeyLen, false),
		text("locale", in.Locale, maxLocaleLen, false),
		text("translation_of", in.TranslationOf, maxSourceKeyLen, false),
	} {
		if c != nil {
			return nil, c
		}
	}
	if len(in.Attributes) > 0 && !isJSONObject(in.Attributes) {
		return nil, invalid("attributes must be a JSON object")
	}
	if in.ParentKey == in.SourceKey && in.ParentKey != "" {
		return nil, invalid("an entity cannot be its own parent")
	}
	if in.TranslationOf != "" {
		switch {
		case in.TranslationOf == in.SourceKey:
			return nil, invalid("an entity cannot be a translation of itself")
		case strings.TrimSpace(in.Locale) == "":
			return nil, invalid("locale is required with translation_of")
		}
	}
	s, err := a.stager(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	if in.TranslationOf != "" {
		if _, isID := parseUUID(in.TranslationOf); !isID {
			ok, err := s.known(ctx, changeset.KindEntity, in.TranslationOf)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, invalid("translation_of %q is neither an entity id, nor published by an earlier run, nor staged earlier in this one; stage the source first", in.TranslationOf)
			}
		}
	}
	if in.ParentKey != "" {
		ok, err := s.known(ctx, changeset.KindEntity, in.ParentKey)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, invalid("parent_key %q is neither published by an earlier run nor staged earlier in this one; stage parents before their children", in.ParentKey)
		}
	}
	p := map[string]any{"schema": in.Schema, "name": in.Name}
	for k, v := range map[string]string{"slug": in.Slug, "parent_key": in.ParentKey, "locale": in.Locale, "translation_of": in.TranslationOf} {
		if v != "" {
			p[k] = v
		}
	}
	// An upsert is the whole document: attributes left out are empty.
	p["attributes"] = json.RawMessage(`{}`)
	if len(in.Attributes) > 0 {
		p["attributes"] = in.Attributes
	}
	if in.SEO != nil {
		p["seo"] = in.SEO
	}
	return s.stage(ctx, changeset.Item{Kind: changeset.KindEntity, Op: changeset.OpUpsert, SourceKey: in.SourceKey, Payload: payload(p)})
}

func parseUUID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	return id, err == nil
}

func isJSONObject(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && m != nil
}

func fnChangesetTermUpsert(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	in, err := decodeArgs[sdk.TermUpsert](raw)
	if err != nil {
		return nil, err
	}
	for _, c := range []error{
		sourceKey(in.SourceKey),
		text("slug", in.Slug, maxSlugLen, true),
		text("name", in.Name, maxNameLen, true),
		text("description", in.Description, 4096, false),
	} {
		if c != nil {
			return nil, c
		}
	}
	if !taxonomy.ValidKind(in.Kind) {
		return nil, invalid("kind %q is not a valid term kind", in.Kind)
	}
	s, err := a.stager(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	return s.stage(ctx, changeset.Item{Kind: changeset.KindTerm, Op: changeset.OpUpsert, SourceKey: in.SourceKey,
		Payload: payload(map[string]string{"kind": in.Kind, "slug": in.Slug, "name": in.Name, "description": in.Description})})
}

// fnChangesetPersonUpsert would stage a byline. Bylines are not a term kind
// in this core (they are person entities attached to content), and this
// function never touches user accounts, so there is nothing safe to stage.
func fnChangesetPersonUpsert(_ context.Context, _ *API, _ *callEnv, raw json.RawMessage) (any, error) {
	in, err := decodeArgs[sdk.PersonUpsert](raw)
	if err != nil {
		return nil, err
	}
	if err := sourceKey(in.SourceKey); err != nil {
		return nil, err
	}
	return nil, herr(sdk.ErrUnavailable, "changeset.person.upsert is not available yet: this core has no byline term kind")
}

func fnChangesetMediaAttach(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	in, err := decodeArgs[sdk.MediaAttach](raw)
	if err != nil {
		return nil, err
	}
	for _, c := range []error{sourceKey(in.SourceKey), text("alt", in.Alt, 1024, false), text("handle", in.Handle, maxPathLen, true)} {
		if c != nil {
			return nil, c
		}
	}
	s, err := a.stager(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(in.Handle, media.StagedPrefix(s.rc.RunID)+"/") || path.Clean(in.Handle) != in.Handle {
		return nil, invalid("handle is not a file this run staged with media.import_url")
	}
	// Media is create-only: a key an earlier run already imported stays as it is.
	if _, ok, err := a.svc.Changesets.Resolve(ctx, e.plugin, changeset.KindMedia, in.SourceKey); err != nil {
		return nil, err
	} else if ok {
		return sdk.UpsertResult{SourceKey: in.SourceKey}, nil
	}
	p := map[string]string{"filename": path.Base(in.Handle)}
	if in.Alt != "" {
		p["alt_text"] = in.Alt
	}
	return s.stage(ctx, changeset.Item{Kind: changeset.KindMedia, Op: changeset.OpUpsert, SourceKey: in.SourceKey,
		BlobKeys: []string{in.Handle}, Payload: payload(p)})
}

func fnChangesetMenuUpsert(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	in, err := decodeArgs[sdk.MenuUpsert](raw)
	if err != nil {
		return nil, err
	}
	for _, c := range []error{sourceKey(in.SourceKey), text("slug", in.Slug, maxSlugLen, true), text("name", in.Name, maxNameLen, true)} {
		if c != nil {
			return nil, c
		}
	}
	var items []menu.ReplaceItem
	keys := map[int]string{}
	if err := flattenMenu(in.Items, -1, 1, &items, keys); err != nil {
		return nil, err
	}
	s, err := a.stager(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		ok, err := s.known(ctx, changeset.KindEntity, k)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, invalid("entity_key %q is neither published by an earlier run nor staged earlier in this one", k)
		}
	}
	if items == nil {
		items = []menu.ReplaceItem{}
	}
	return s.stage(ctx, changeset.Item{Kind: changeset.KindMenu, Op: changeset.OpUpsert, SourceKey: in.SourceKey,
		Payload: payload(map[string]any{"slug": in.Slug, "name": in.Name, "items": items, "entity_keys": keys})})
}

// flattenMenu turns the item tree into the parent-index list the menu service
// takes, depth first so a parent always precedes its children. Items linking
// an entity record its source key in keys by index; the engine resolves it at
// publish.
func flattenMenu(in []sdk.MenuItem, parent, depth int, out *[]menu.ReplaceItem, keys map[int]string) error {
	if depth > maxMenuDepth {
		return invalid("menu nested deeper than %d levels", maxMenuDepth)
	}
	for _, it := range in {
		if len(*out) >= maxMenuItems {
			return invalid("menu has more than %d items", maxMenuItems)
		}
		if err := text("label", it.Label, maxLabelLen, true); err != nil {
			return err
		}
		ri := menu.ReplaceItem{Label: it.Label, LinkType: menu.LinkTypeNone, Parent: parent}
		switch {
		case it.URL != "" && it.EntityKey != "":
			return invalid("menu item %q has both url and entity_key", it.Label)
		case it.URL != "":
			if err := text("url", it.URL, maxPathLen, true); err != nil {
				return err
			}
			u := it.URL
			switch {
			case strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://"):
				ri.LinkType = menu.LinkTypeExternal
			case strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//"):
				ri.LinkType = menu.LinkTypeCustom
			default:
				return invalid("menu item %q: url must be an http(s) address or a path starting with /", it.Label)
			}
			ri.URL = &u
		case it.EntityKey != "":
			if err := text("entity_key", it.EntityKey, maxSourceKeyLen, true); err != nil {
				return err
			}
			ri.LinkType = menu.LinkTypeEntity
			keys[len(*out)] = it.EntityKey
		}
		idx := len(*out)
		*out = append(*out, ri)
		if err := flattenMenu(it.Children, idx, depth+1, out, keys); err != nil {
			return err
		}
	}
	return nil
}

func fnChangesetRedirectUpsert(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	in, err := decodeArgs[sdk.RedirectUpsert](raw)
	if err != nil {
		return nil, err
	}
	for _, c := range []error{sourceKey(in.SourceKey), text("from", in.From, maxPathLen, true), text("to", in.To, maxPathLen, true)} {
		if c != nil {
			return nil, c
		}
	}
	for field, v := range map[string]string{"from": in.From, "to": in.To} {
		if !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") {
			return nil, invalid("%s must be a path on this site, starting with /", field)
		}
	}
	status := in.StatusCode
	if status == 0 {
		status = 301
	}
	if !redirect.ValidStatusCode(status) {
		return nil, invalid("status_code must be 301, 302, 307 or 308")
	}
	s, err := a.stager(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	if a.svc.LivePath != nil && a.svc.LivePath(ctx, in.From) {
		return nil, invalid("from %q is served by live content; a redirect may not shadow it", in.From)
	}
	return s.stage(ctx, changeset.Item{Kind: changeset.KindRedirect, Op: changeset.OpUpsert, SourceKey: in.SourceKey,
		Payload: payload(map[string]any{"source_path": in.From, "target": in.To, "status": status})})
}

// deletable are the kinds changeset.delete may remove; media is create-only.
var deletable = []changeset.Kind{changeset.KindEntity, changeset.KindTerm, changeset.KindMenu, changeset.KindRedirect}

func fnChangesetDelete(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	in, err := decodeArgs[sdk.ChangesetDeleteArgs](raw)
	if err != nil {
		return nil, err
	}
	if err := sourceKey(in.SourceKey); err != nil {
		return nil, err
	}
	kind := changeset.Kind(in.Kind)
	if !slices.Contains(deletable, kind) {
		return nil, invalid("kind must be one of entity, term, menu or redirect")
	}
	s, err := a.stager(ctx, e.plugin)
	if err != nil {
		return nil, err
	}
	target, ok, err := a.svc.Changesets.Resolve(ctx, e.plugin, kind, in.SourceKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, herr(sdk.ErrNotFound, "no %s was published under source_key %q", kind, in.SourceKey)
	}
	if err := a.svc.Changesets.Stage(ctx, s.csID, changeset.Item{Kind: kind, Op: changeset.OpDelete, SourceKey: in.SourceKey, TargetID: target}); err != nil {
		return nil, err
	}
	return sdk.UpsertResult{SourceKey: in.SourceKey}, nil
}

// Proposal limits.
const (
	maxSiteTitleLen   = 200
	maxSiteTaglineLen = 500
)

func fnChangesetPropose(ctx context.Context, a *API, e *callEnv, raw json.RawMessage) (any, error) {
	in, err := decodeArgs[sdk.Proposal](raw)
	if err != nil {
		return nil, err
	}
	rc, ok := RunFrom(ctx)
	if !ok {
		return nil, herr(sdk.ErrPhaseDenied, "only available inside a run")
	}
	if !slices.Contains(rc.Proposes, in.Kind) {
		return nil, invalid("run %q did not declare it proposes %q", rc.Name, in.Kind)
	}
	doc, err := checkProposal(in.Kind, in.Payload)
	if err != nil {
		return nil, err
	}
	_, runs, err := a.runStore(ctx)
	if err != nil {
		return nil, err
	}
	if err := runs.AddProposal(ctx, rc.RunID, in.Kind, doc); err != nil {
		return nil, runErr(err)
	}
	return struct{}{}, nil
}

// checkProposal validates a proposal against the allow-list and returns the
// document to store. site_settings is limited to title, tagline, time zone and
// language; the other kinds are stored as a JSON object and checked by the
// owner at sign-off, which is also where a proposal is applied.
func checkProposal(kind string, payload json.RawMessage) (json.RawMessage, error) {
	if !isJSONObject(payload) {
		return nil, invalid("payload must be a JSON object")
	}
	if kind != sdk.ProposeSiteSettings {
		return payload, nil
	}
	m, err := decodeArgs[map[string]string](payload)
	if err != nil {
		return nil, invalid("site_settings values must be strings")
	}
	if len(m) == 0 {
		return nil, invalid("site_settings proposes nothing")
	}
	for k, v := range m {
		var err error
		switch k {
		case "title":
			err = text(k, v, maxSiteTitleLen, true)
		case "tagline":
			err = text(k, v, maxSiteTaglineLen, false)
		case "time_zone":
			if _, lerr := time.LoadLocation(v); lerr != nil || v == "" || v == "Local" {
				err = invalid("time_zone %q is not a time zone name", v)
			}
		case "language":
			err = text(k, v, maxLocaleLen, true)
		default:
			err = invalid("site_settings may propose title, tagline, time_zone and language, not %q", k)
		}
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(m)
}
