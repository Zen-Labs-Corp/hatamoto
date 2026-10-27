package entity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MaxImportBatch bounds the items of one Import call; bigger sources are sent
// in several batches (each emits its own import.completed).
const MaxImportBatch = 1000

// Import errors.
var (
	ErrImportUnsupported = errors.New("the entity repository does not support import")
	ErrImportEmpty       = errors.New("import batch is empty")
	ErrImportTooLarge    = fmt.Errorf("import batch has more than %d items", MaxImportBatch)
)

// ImportItem is one entity to import.
type ImportItem struct {
	// Key is the idempotency key, unique per source entity (for example
	// "wp:post:123"). An item whose key was imported before updates that
	// entity in place instead of creating another.
	Key        string
	SchemaSlug string
	Name       string
	Slug       string // generated from Name when empty
	Locale     string // BCP 47 tag; empty is the site language on insert and unchanged on update
	Status     EntityStatus
	Attributes json.RawMessage
	Metadata   json.RawMessage

	// ParentKey names the parent by import key (a row of this batch or of an
	// earlier one); ParentID by id. At most one is set.
	ParentKey string
	ParentID  *uuid.UUID

	// AuthorID is a legacy hatamoto_authors id; AuthorName resolves a person by name.
	// Without either, a plain-text attributes.author is resolved like a save.
	AuthorID   *uuid.UUID
	AuthorName string

	// BylinePersonID credits a person entity; BylineOrg the site organisation.
	BylinePersonID *uuid.UUID
	BylineOrg      bool

	// Dates; zero values default to now (created/updated) and, for a
	// published item, to its creation date (published).
	PublishedAt, CreatedAt, UpdatedAt time.Time
}

// ImportBatch is what one Import call stores.
type ImportBatch struct {
	// Source labels the origin ("wordpress", "archive"); it is stored in the
	// entity metadata and sent in import.completed.
	Source string
	Items  []ImportItem
}

// ImportItemResult is the outcome of one item.
type ImportItemResult struct {
	Key     string
	ID      uuid.UUID
	Created bool
	Err     error
}

// ImportResult summarises a batch. Items are in the order of the input.
type ImportResult struct {
	Created, Updated, Failed int
	Items                    []ImportItemResult
}

// ImportSummary is the payload of the import.completed event.
type ImportSummary struct {
	Source                   string
	Created, Updated, Failed int
}

// Import stores a batch of entities without any per-entity event: it sets
// dates, status, parent and author, validates attributes against the schema,
// sanitises them like a normal save, and emits ONE import.completed event for
// the whole batch (never entity.created / entity.published). Items fail
// individually (see ImportResult); the call itself fails only for an empty or
// oversized batch, or a repository without import support.
func (s *Service) Import(ctx context.Context, actor *uuid.UUID, batch ImportBatch) (*ImportResult, error) {
	store, ok := s.entityRepo.(ImportStore)
	if !ok {
		return nil, ErrImportUnsupported
	}
	if len(batch.Items) == 0 {
		return nil, ErrImportEmpty
	}
	if len(batch.Items) > MaxImportBatch {
		return nil, ErrImportTooLarge
	}

	res := &ImportResult{Items: make([]ImportItemResult, len(batch.Items))}
	for i, it := range batch.Items {
		res.Items[i].Key = it.Key
	}
	run := &importRun{s: s, store: store, actor: actor, source: batch.Source, ids: map[string]uuid.UUID{}, schemas: map[string]*EntitySchema{}}
	var stored []uuid.UUID
	for _, i := range orderByParent(batch.Items, res.Items) {
		r := &res.Items[i]
		if r.Err != nil {
			continue
		}
		e, created, err := run.item(ctx, &batch.Items[i])
		if err != nil {
			r.Err = err
			continue
		}
		r.ID, r.Created = e.ID, created
		run.ids[batch.Items[i].Key] = e.ID
		stored = append(stored, e.ID)
	}
	for _, r := range res.Items {
		switch {
		case r.Err != nil:
			res.Failed++
		case r.Created:
			res.Created++
		default:
			res.Updated++
		}
	}

	s.logger.Info("imported entities",
		slog.String("source", batch.Source), slog.Int("created", res.Created),
		slog.Int("updated", res.Updated), slog.Int("failed", res.Failed))
	s.reindexLinks(stored)
	s.fireLive(ctx, stored...)
	if s.events != nil {
		s.events.Emit(ctx, Event{Kind: EventImportCompleted, Import: &ImportSummary{
			Source: batch.Source, Created: res.Created, Updated: res.Updated, Failed: res.Failed,
		}})
	}
	return res, nil
}

// reindexLinks refreshes the link index of imported rows in the background.
func (s *Service) reindexLinks(ids []uuid.UUID) {
	if len(ids) == 0 || s.linkSync == nil {
		return
	}
	go func() {
		for _, id := range ids {
			if err := s.linkSync.Reindex(context.Background(), id); err != nil {
				s.logger.Warn("link sync reindex failed", slog.String("entity_id", id.String()), slog.String("error", err.Error()))
			}
		}
	}()
}

// orderByParent returns item indexes with every parent (by key, inside the
// batch) before its children. Items in a parent cycle, or with an empty key,
// get an error in results and are left out of the order.
func orderByParent(items []ImportItem, results []ImportItemResult) []int {
	index := map[string]int{}
	for i, it := range items {
		switch {
		case it.Key == "":
			results[i].Err = errors.New("import key is required")
		default:
			if _, dup := index[it.Key]; dup {
				results[i].Err = fmt.Errorf("import key %q appears twice in the batch", it.Key)
				continue
			}
			index[it.Key] = i
		}
	}
	const (
		visiting = 1
		done     = 2
	)
	state := make([]int, len(items))
	var order []int
	var visit func(i int) bool
	visit = func(i int) bool {
		switch state[i] {
		case done:
			return results[i].Err == nil
		case visiting:
			return false // cycle
		}
		state[i] = visiting
		ok := results[i].Err == nil
		if p, in := index[items[i].ParentKey]; ok && items[i].ParentKey != "" && in {
			if !visit(p) {
				if results[i].Err == nil {
					results[i].Err = fmt.Errorf("parent %q: %w", items[i].ParentKey, ErrParentCycle)
				}
				ok = false
			}
		}
		state[i] = done
		if ok {
			order = append(order, i)
		}
		return ok
	}
	for i := range items {
		visit(i)
	}
	return order
}

type importRun struct {
	s       *Service
	store   ImportStore
	actor   *uuid.UUID
	source  string
	ids     map[string]uuid.UUID // key -> entity id, for parents inside the batch
	schemas map[string]*EntitySchema
}

func (r *importRun) schema(ctx context.Context, slug string) (*EntitySchema, error) {
	if sc, ok := r.schemas[slug]; ok {
		return sc, nil
	}
	sc, err := r.s.schemaRepo.GetBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	r.schemas[slug] = sc
	return sc, nil
}

// item validates, sanitises and stores one item.
func (r *importRun) item(ctx context.Context, it *ImportItem) (*Entity, bool, error) {
	s := r.s
	schema, err := r.schema(ctx, it.SchemaSlug)
	if err != nil {
		return nil, false, fmt.Errorf("schema %q: %w", it.SchemaSlug, err)
	}
	if strings.TrimSpace(it.Name) == "" {
		return nil, false, errors.New("name is required")
	}
	status := it.Status
	if status == "" {
		status = StatusDraft
	}
	if !ValidStatus(status) {
		return nil, false, ErrInvalidStatus
	}
	if status == StatusScheduled && it.PublishedAt.IsZero() {
		return nil, false, errors.New("a scheduled item needs published_at")
	}
	locale := it.Locale
	if locale != "" {
		if locale, err = NormalizeLocale(locale); err != nil {
			return nil, false, err
		}
	}
	attrs := it.Attributes
	if len(attrs) == 0 {
		attrs = json.RawMessage(`{}`)
	}
	if err := s.validator.ValidateAttributes(schema.Schema, attrs); err != nil {
		return nil, false, err
	}
	if attrs, err = sanitizeAttributes(attrs); err != nil {
		return nil, false, err
	}

	slug := normalizeSlug(it.Slug)
	if slug == "" {
		slug = generateSlug(it.Name)
	}
	if slug == "" {
		return nil, false, errors.New("slug is empty")
	}
	name := it.Name

	existing, err := r.store.GetByImportKey(ctx, it.Key)
	switch {
	case errors.Is(err, ErrEntityNotFound):
		existing = nil
	case err != nil:
		return nil, false, err
	}

	var stored json.RawMessage
	if existing != nil {
		stored = existing.Attributes
	}
	if attrs, err = normalizeDocRaw(docWrite, stored, attrs, EligibleForPageDoc(schema)); err != nil {
		return nil, false, err
	}

	if s.beforeSave != nil {
		op := "create"
		var id *uuid.UUID
		if existing != nil {
			op, id = "update", &existing.ID
		}
		draftLocale := locale
		if draftLocale == "" && existing != nil {
			draftLocale = existing.Locale
		}
		draft := &BeforeSaveInput{Operation: op, ID: id, SchemaSlug: schema.Slug, Name: name, Slug: slug, Locale: draftLocale, Attributes: attrs}
		if attrs, err = s.runBeforeSave(ctx, draft, schema); err != nil {
			return nil, false, err
		}
		name = draft.Name
		if draft.Slug != "" {
			slug = normalizeSlug(draft.Slug)
		}
	}

	parent, err := r.parent(ctx, it, existingID(existing))
	if err != nil {
		return nil, false, err
	}
	byline, authorID, authorName, err := r.credit(ctx, it, attrs)
	if err != nil {
		return nil, false, err
	}
	// The byline text is only written where the schema accepts it: one that
	// forbids extra properties and has no "author" would reject the very next
	// normal save of this entity. The author row (author_id) is set either way.
	if authorName != "" && bylineOf(attrs) == "" && schemaAllowsProperty(schema.Schema, "author") {
		if attrs, err = setAttribute(attrs, "author", authorName); err != nil {
			return nil, false, err
		}
	}

	// A person's status follows their Public setting, a block or theme part is
	// always a draft, whatever the source says.
	if st := lockedStatus(schema, attrs); st != nil {
		status = *st
	}

	meta, err := importMetadata(it.Metadata, it.Key, r.source)
	if err != nil {
		return nil, false, err
	}
	rec := &ImportRecord{
		SchemaID: schema.ID, Name: name, Slug: slug, Locale: locale, Status: status, Attributes: attrs, Metadata: meta,
		ParentID: parent, AuthorID: authorID, ActorID: r.actor,
		BylinePersonID: byline.PersonID, BylineOrg: byline.Org,
	}
	if existing != nil {
		rec.ID = &existing.ID
	}
	rec.CreatedAt, rec.UpdatedAt, rec.PublishedAt = importDates(it, status, time.Now())

	e, created, err := r.store.ImportUpsert(ctx, rec)
	if err != nil {
		return nil, false, err
	}
	e.Schema = schema
	return e, created, nil
}

func existingID(e *Entity) *uuid.UUID {
	if e == nil {
		return nil
	}
	return &e.ID
}

// parent resolves the item's parent and checks it.
func (r *importRun) parent(ctx context.Context, it *ImportItem, self *uuid.UUID) (*uuid.UUID, error) {
	var id uuid.UUID
	switch {
	case it.ParentKey != "" && it.ParentID != nil:
		return nil, errors.New("set parent_key or parent_id, not both")
	case it.ParentID != nil:
		id = *it.ParentID
	case it.ParentKey != "":
		if pid, ok := r.ids[it.ParentKey]; ok {
			id = pid
			break
		}
		p, err := r.store.GetByImportKey(ctx, it.ParentKey)
		if err != nil {
			if errors.Is(err, ErrEntityNotFound) {
				return nil, fmt.Errorf("parent %q: %w", it.ParentKey, ErrParentNotFound)
			}
			return nil, err
		}
		id = p.ID
	default:
		return nil, nil
	}
	if err := r.s.checkParent(ctx, self, id); err != nil {
		return nil, err
	}
	return &id, nil
}

// credit resolves the item's byline: an explicit person or organisation, else
// the byline text (an AuthorName, else attributes.author). name is the display
// name to write into attributes.author when the item has none.
func (r *importRun) credit(ctx context.Context, it *ImportItem, attrs json.RawMessage) (b Byline, authorID *uuid.UUID, name string, err error) {
	s := r.s
	if s.bylines == nil {
		return b, it.AuthorID, "", nil
	}
	switch {
	case it.BylinePersonID != nil:
		if err = s.checkPerson(ctx, *it.BylinePersonID); err != nil {
			return
		}
		b.PersonID = it.BylinePersonID
	case it.BylineOrg:
		b.Org = true
	case it.AuthorID != nil:
		// A legacy author id is stored as given; the foreign key verifies it.
		return b, it.AuthorID, "", nil
	case strings.TrimSpace(it.AuthorName) != "":
		if b, err = s.bylines.Resolve(ctx, it.AuthorName); err != nil {
			return
		}
		name = strings.TrimSpace(it.AuthorName)
	default:
		var ok bool
		if b, ok, err = s.bylineFromText(ctx, attrs); err != nil || !ok {
			return b, nil, "", err
		}
	}
	if b.PersonID != nil {
		authorID, err = s.mirrorOf(ctx, *b.PersonID)
	}
	return b, authorID, name, err
}

// importDates fills the dates of an item: created defaults to now, updated to
// created, published (for a published or scheduled item) to created.
func importDates(it *ImportItem, status EntityStatus, now time.Time) (created, updated, published time.Time) {
	created = it.CreatedAt
	if created.IsZero() {
		created = now
	}
	updated = it.UpdatedAt
	if updated.IsZero() || updated.Before(created) {
		updated = created
	}
	published = it.PublishedAt
	if published.IsZero() && (status == StatusPublished || status == StatusArchived) {
		published = created
	}
	return created.UTC(), updated.UTC(), published.UTC()
}

// importMetadata returns metadata carrying the import key and source.
func importMetadata(raw json.RawMessage, key, source string) (json.RawMessage, error) {
	m := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("metadata must be a JSON object: %w", err)
		}
	}
	m["import_key"] = key
	if source != "" {
		m["import_source"] = source
	}
	return json.Marshal(m)
}

// setAttribute sets one top-level attribute.
func setAttribute(attrs json.RawMessage, key string, value any) (json.RawMessage, error) {
	m := map[string]any{}
	if err := json.Unmarshal(attrs, &m); err != nil {
		return nil, err
	}
	m[key] = value
	return json.Marshal(m)
}

// schemaAllowsProperty reports whether a JSON Schema document lets objects
// carry the property: it is declared, or additionalProperties is not false.
func schemaAllowsProperty(schema json.RawMessage, name string) bool {
	var doc struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	}
	if json.Unmarshal(schema, &doc) != nil {
		return true
	}
	if _, ok := doc.Properties[name]; ok {
		return true
	}
	return strings.TrimSpace(string(doc.AdditionalProperties)) != "false"
}
