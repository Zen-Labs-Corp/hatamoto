package changeset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/menu"
	"hatamoto.dev/core/internal/redirect"
	"hatamoto.dev/core/internal/taxonomy"
)

// Conflict reasons the writers report in Outcome.Detail.
const (
	reasonMoved = "moved"
	reasonGone  = "gone"
	// reasonLocation: the menu is assigned to a location, which only the owner changes.
	reasonLocation = "has_location"
	// reasonRevertMoved is why a revert left an object in place.
	reasonRevertMoved = "changed_since_publish"
)

func parseTarget(target string) (*uuid.UUID, error) {
	if target == "" {
		return nil, nil
	}
	id, err := uuid.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("%w: target %q is not an id", ErrInvalidItem, target)
	}
	return &id, nil
}

func decodePayload(raw json.RawMessage, dst any) error {
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%w: payload: %v", ErrInvalidItem, err)
	}
	return nil
}

// parseTime reads a base token time; empty is nil (no check).
func parseTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil, fmt.Errorf("%w: base token %q", ErrInvalidItem, s)
	}
	return &t, nil
}

// EntityToken is the base token of an entity: "version:updated_at".
func EntityToken(version int, updatedAt time.Time) string {
	return strconv.Itoa(version) + ":" + updatedAt.UTC().Format(time.RFC3339Nano)
}

// TimeToken is the base token of every other kind: updated_at.
func TimeToken(updatedAt time.Time) string { return updatedAt.UTC().Format(time.RFC3339Nano) }

func parseEntityToken(s string) (*int, *time.Time, error) {
	if s == "" {
		return nil, nil, nil
	}
	v, ts, ok := strings.Cut(s, ":")
	n, err := strconv.Atoi(v)
	if !ok || err != nil {
		return nil, nil, fmt.Errorf("%w: base token %q", ErrInvalidItem, s)
	}
	t, err := parseTime(ts)
	if err != nil {
		return nil, nil, err
	}
	return &n, t, nil
}

// EntityWriter adapts entity.Service.
type EntityWriter struct{ Svc *entity.Service }

type entityPayload struct {
	Schema     string          `json:"schema"`
	Name       string          `json:"name"`
	Slug       string          `json:"slug"`
	Locale     string          `json:"locale"`
	Attributes json.RawMessage `json:"attributes"`
	ParentID   *uuid.UUID      `json:"parent_id"`
	Status     string          `json:"status"`
}

func (w EntityWriter) Apply(ctx context.Context, tx Tx, origin string, it Item, target string) (Outcome, error) {
	var p entityPayload
	if err := decodePayload(it.Payload, &p); err != nil {
		return Outcome{}, err
	}
	id, err := parseTarget(target)
	if err != nil {
		return Outcome{}, err
	}
	ver, at, err := parseEntityToken(it.BaseToken)
	if err != nil {
		return Outcome{}, err
	}
	op := entity.TxOp{Schema: p.Schema, ID: id, Name: p.Name, Slug: p.Slug, Locale: p.Locale, Attributes: p.Attributes,
		ParentID: p.ParentID, Status: entity.EntityStatus(p.Status), Origin: origin, Delete: it.Op == OpDelete,
		BaseVersion: ver, BaseUpdatedAt: at}
	res, err := w.Svc.ApplyChangesetTx(ctx, tx, op)
	switch {
	case errors.Is(err, entity.ErrConflict):
		return Outcome{Conflict: true, Detail: reasonMoved}, nil
	case errors.Is(err, entity.ErrEntityNotFound) && id != nil:
		return Outcome{Conflict: true, Detail: reasonGone}, nil
	case err != nil:
		return Outcome{}, err
	}
	return Outcome{TargetID: res.ID.String(), Before: res.Before, Created: res.Created, Deleted: res.Deleted, PathMoved: res.PathMoved}, nil
}

func (w EntityWriter) Restore(ctx context.Context, tx Tx, it StoredItem) (func(context.Context), error) {
	id, err := uuid.Parse(it.TargetID)
	if err != nil {
		return nil, fmt.Errorf("%w: target %q", ErrInvalidItem, it.TargetID)
	}
	// An overwritten row may move back to another path: remember where it is.
	var post func(context.Context)
	if len(it.Before) > 0 {
		if prev, err := w.Svc.PathStateTx(ctx, tx, id); err == nil && moves(prev, it.Before) {
			post = func(ctx context.Context) { w.Svc.AfterPathChange(ctx, []entity.PathState{prev}) }
		}
	}
	err = w.Svc.RestoreChangesetTx(ctx, tx, id, it.Before)
	if errors.Is(err, entity.ErrHasChildren) {
		return nil, fmt.Errorf("%w: %v", ErrRevertConflict, err)
	}
	return post, err
}

// moves reports whether restoring the pre-image puts the entity at another path than live.
func moves(live entity.PathState, before json.RawMessage) bool {
	to, err := entity.PathStateOf(before)
	return err == nil && live.Moved(to)
}

func (w EntityWriter) After(ctx context.Context, applied []AppliedItem) {
	var created, updated, deleted []uuid.UUID
	var prev []entity.PathState
	for _, a := range applied {
		id, err := uuid.Parse(a.TargetID)
		if err != nil {
			continue
		}
		switch {
		case a.Created:
			created = append(created, id)
		case a.Deleted:
			deleted = append(deleted, id)
		default:
			updated = append(updated, id)
			if a.PathMoved && len(a.Before) > 0 {
				if st, err := entity.PathStateOf(a.Before); err == nil {
					prev = append(prev, st)
				}
			}
		}
	}
	w.Svc.AfterChangeset(ctx, created, updated, deleted, prev)
}

// TermWriter adapts the taxonomy package; Svc is notified after the commit.
type TermWriter struct{ Svc *taxonomy.Service }

type termPayload struct {
	Kind        string          `json:"kind"`
	Slug        string          `json:"slug"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	ParentID    *uuid.UUID      `json:"parent_id"`
	Locale      string          `json:"locale"`
	SEO         json.RawMessage `json:"seo"`
}

func (w TermWriter) Apply(ctx context.Context, tx Tx, origin string, it Item, target string) (Outcome, error) {
	var p termPayload
	if err := decodePayload(it.Payload, &p); err != nil {
		return Outcome{}, err
	}
	id, err := parseTarget(target)
	if err != nil {
		return Outcome{}, err
	}
	at, err := parseTime(it.BaseToken)
	if err != nil {
		return Outcome{}, err
	}
	res, err := taxonomy.ApplyChangesetTx(ctx, tx, taxonomy.TermOp{ID: id, Kind: p.Kind, Slug: p.Slug, Name: p.Name,
		Description: p.Description, ParentID: p.ParentID, Locale: p.Locale, SEO: p.SEO, BaseUpdatedAt: at,
		Origin: origin, Delete: it.Op == OpDelete})
	switch {
	case errors.Is(err, taxonomy.ErrTermConflict):
		return Outcome{Conflict: true, Detail: reasonMoved}, nil
	case errors.Is(err, taxonomy.ErrTermNotFound) && id != nil:
		return Outcome{Conflict: true, Detail: reasonGone}, nil
	case err != nil:
		return Outcome{}, err
	}
	return Outcome{TargetID: res.ID.String(), Before: res.Before, Created: res.Created, Deleted: res.Deleted}, nil
}

func (w TermWriter) Restore(ctx context.Context, tx Tx, it StoredItem) (func(context.Context), error) {
	if it.Before == nil {
		id, err := parseTarget(it.TargetID)
		if err != nil || id == nil {
			return nil, fmt.Errorf("%w: target %q", ErrInvalidItem, it.TargetID)
		}
		_, err = taxonomy.ApplyChangesetTx(ctx, tx, taxonomy.TermOp{ID: id, Delete: true})
		return nil, err
	}
	return nil, taxonomy.RestoreChangesetTx(ctx, tx, it.Before)
}

func (w TermWriter) After(ctx context.Context, _ []AppliedItem) { w.Svc.NotifyChanged(ctx) }

// RedirectWriter adapts the redirect package; the resolver cache is
// invalidated after the commit.
type RedirectWriter struct {
	Svc          *redirect.Service
	SameHostOnly bool
	// SourceLive reports whether a path is served by a live page (see
	// redirect.RedirectOp); nil skips the check.
	SourceLive func(ctx context.Context, path string) (bool, error)
}

type redirectPayload struct {
	SourcePath string `json:"source_path"`
	Target     string `json:"target"`
	Status     int    `json:"status"`
}

func (w RedirectWriter) Apply(ctx context.Context, tx Tx, origin string, it Item, target string) (Outcome, error) {
	var p redirectPayload
	if err := decodePayload(it.Payload, &p); err != nil {
		return Outcome{}, err
	}
	id, err := parseTarget(target)
	if err != nil {
		return Outcome{}, err
	}
	at, err := parseTime(it.BaseToken)
	if err != nil {
		return Outcome{}, err
	}
	res, err := redirect.ApplyChangesetTx(ctx, tx, redirect.RedirectOp{ID: id, SourcePath: p.SourcePath, Target: p.Target,
		Status: p.Status, BaseUpdatedAt: at, Origin: origin, Delete: it.Op == OpDelete, SameHostOnly: w.SameHostOnly, SourceLive: w.SourceLive})
	if err != nil {
		return Outcome{}, err
	}
	if res.Conflict != "" {
		return Outcome{Conflict: true, Detail: res.Conflict}, nil
	}
	out := Outcome{TargetID: res.ID.String(), Created: id == nil, Deleted: it.Op == OpDelete}
	if res.Before != nil {
		if out.Before, err = json.Marshal(res.Before); err != nil {
			return Outcome{}, err
		}
	}
	return out, nil
}

func (w RedirectWriter) Restore(ctx context.Context, tx Tx, it StoredItem) (func(context.Context), error) {
	id, err := parseTarget(it.TargetID)
	if err != nil || id == nil {
		return nil, fmt.Errorf("%w: target %q", ErrInvalidItem, it.TargetID)
	}
	if it.Before == nil {
		res, err := redirect.ApplyChangesetTx(ctx, tx, redirect.RedirectOp{ID: id, Delete: true})
		if err == nil && res.Conflict != "" && res.Conflict != redirect.ConflictGone {
			err = fmt.Errorf("redirect not removed: %s", res.Conflict)
		}
		return nil, err
	}
	var pre redirect.Preimage
	if err := json.Unmarshal(it.Before, &pre); err != nil {
		return nil, fmt.Errorf("redirect pre-image: %w", err)
	}
	res, err := redirect.RestoreChangesetTx(ctx, tx, pre)
	if err == nil && res.Conflict != "" {
		err = fmt.Errorf("redirect not restored: %s", res.Conflict)
	}
	return nil, err
}

func (w RedirectWriter) After(context.Context, []AppliedItem) { w.Svc.InvalidateCache() }

// MenuWriter adapts menu.Service.
type MenuWriter struct{ Svc *menu.Service }

type menuPayload struct {
	Slug  string             `json:"slug"`
	Name  string             `json:"name"`
	Items []menu.ReplaceItem `json:"items"`
}

func (w MenuWriter) Apply(ctx context.Context, tx Tx, origin string, it Item, target string) (Outcome, error) {
	var p menuPayload
	if err := decodePayload(it.Payload, &p); err != nil {
		return Outcome{}, err
	}
	id, err := parseTarget(target)
	if err != nil {
		return Outcome{}, err
	}
	at, err := parseTime(it.BaseToken)
	if err != nil {
		return Outcome{}, err
	}
	res, err := w.Svc.ApplyChangesetTx(ctx, tx, menu.MenuOp{ID: id, Slug: p.Slug, Name: p.Name, Items: p.Items,
		BaseUpdatedAt: at, Origin: origin, Delete: it.Op == OpDelete})
	switch {
	case errors.Is(err, menu.ErrMenuNotFound) && id != nil:
		return Outcome{Conflict: true, Detail: reasonGone}, nil
	case errors.Is(err, menu.ErrMenuHasLocation):
		return Outcome{Conflict: true, Detail: reasonLocation}, nil
	case err != nil:
		return Outcome{}, err
	case res.Conflict:
		return Outcome{Conflict: true, Detail: reasonMoved}, nil
	}
	return Outcome{TargetID: res.ID.String(), Before: res.Before, Created: res.Created, Deleted: it.Op == OpDelete}, nil
}

func (w MenuWriter) Restore(ctx context.Context, tx Tx, it StoredItem) (func(context.Context), error) {
	if it.Before == nil {
		id, err := parseTarget(it.TargetID)
		if err != nil || id == nil {
			return nil, fmt.Errorf("%w: target %q", ErrInvalidItem, it.TargetID)
		}
		_, err = w.Svc.ApplyChangesetTx(ctx, tx, menu.MenuOp{ID: id, Delete: true})
		return nil, err
	}
	return nil, w.Svc.RestoreChangesetTx(ctx, tx, it.Before)
}

func (w MenuWriter) After(ctx context.Context, _ []AppliedItem) { w.Svc.NotifyChanged(ctx) }

// MediaWriter adapts media.Service: an item publishes one staged file as a
// new media record (media is create-only in a changeset).
type MediaWriter struct{ Svc *media.Service }

type mediaPayload struct {
	Filename string  `json:"filename"`
	AltText  *string `json:"alt_text"`
	Caption  *string `json:"caption"`
	Folder   string  `json:"folder"`
}

func (w MediaWriter) Apply(ctx context.Context, tx Tx, origin string, it Item, target string) (Outcome, error) {
	if it.Op == OpDelete || target != "" || len(it.BlobKeys) != 1 {
		return Outcome{}, ErrUnsupportedOp
	}
	var p mediaPayload
	if err := decodePayload(it.Payload, &p); err != nil {
		return Outcome{}, err
	}
	m, err := w.Svc.AttachStagedTx(ctx, tx, media.MediaOp{StagedKey: it.BlobKeys[0], Filename: p.Filename,
		Origin: origin, AltText: p.AltText, Caption: p.Caption, Folder: p.Folder})
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{TargetID: m.ID.String(), Created: true, Undo: func(ctx context.Context) { w.Svc.RemoveFiles(ctx, m) }}, nil
}

// Restore removes the record a changeset created, and its files after the commit.
func (w MediaWriter) Restore(ctx context.Context, tx Tx, it StoredItem) (func(context.Context), error) {
	id, err := uuid.Parse(it.TargetID)
	if err != nil {
		return nil, fmt.Errorf("%w: target %q", ErrInvalidItem, it.TargetID)
	}
	m, err := w.Svc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM hatamoto_media WHERE id = $1`, id); err != nil {
		return nil, fmt.Errorf("failed to delete media: %w", err)
	}
	return func(ctx context.Context) { w.Svc.RemoveFiles(ctx, m) }, nil
}

func (w MediaWriter) After(context.Context, []AppliedItem) {}
