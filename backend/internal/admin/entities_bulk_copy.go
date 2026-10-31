package admin

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
)

const bulkCopyPrefix = "copy:"

// bulkCopyActions are the "Create drafts in <locale>" bulk actions: one per
// site locale, for people who may edit translations on a multi-locale site.
func (a *Admin) bulkCopyActions(r *http.Request) []ui.BulkAction {
	set, ok := a.multiLocale(r.Context())
	if !ok || a.deps.Content.Copier == nil || !canEditTranslations(r) {
		return nil
	}
	var acts []ui.BulkAction
	for _, tag := range set.Tags() {
		acts = append(acts, ui.BulkAction{Label: "Create drafts in " + tag, Action: a.adminPath + "/entities/bulk", Value: bulkCopyPrefix + tag})
	}
	return acts
}

// bulkCopy creates a draft copy of each entity in the chosen locale. Each row
// stands alone: one that cannot be copied is counted, never stops the rest.
func (a *Admin) bulkCopy(w http.ResponseWriter, r *http.Request, action string, ids []string, back string) {
	set, ok := a.multiLocale(r.Context())
	tag := strings.TrimPrefix(action, bulkCopyPrefix)
	if !ok || a.deps.Content.Copier == nil || !canEditTranslations(r) || !slices.Contains(set.Tags(), tag) {
		a.SetFlash(w, r, "error", "Choose an action for the selected entities.")
		Redirect(w, r, back)
		return
	}
	uid := uuid.Nil
	if u := actorID(r); u != nil {
		uid = *u
	}
	created, taken, unsupported, failed := 0, 0, 0, 0
	var firstErr string
	for _, raw := range ids {
		id, err := uuid.Parse(raw)
		if err != nil {
			failed++
			continue
		}
		src, err := a.deps.Content.Entities.GetEntity(r.Context(), id)
		if err != nil {
			failed++
			continue
		}
		if src.Locale == tag {
			taken++
			continue
		}
		cp, err := a.deps.Content.Copier.CreateCopy(r.Context(), uid, id, entity.CopyOptions{
			Locale: tag, DefaultLocale: set.Default(), CreateParents: true, AutoSlug: true, From: entity.CopyFromThis,
		})
		switch {
		case errors.Is(err, entity.ErrLocaleTaken):
			taken++
		case errors.Is(err, entity.ErrCopyUnsupported):
			unsupported++
		case err != nil:
			failed++
			if firstErr == "" {
				firstErr = entityErrorText(err)
			}
		default:
			created++
			a.auditEntity(r, auditEntityCreateCopy, cp.ID, cp, map[string]any{"source": id.String(), "locale": tag, "bulk": true})
		}
	}
	if created > 0 {
		a.invalidate(r.Context())
	}
	skipped := taken + unsupported
	var msg, kind string
	switch {
	case created == 0 && failed == 0:
		msg, kind = nothingCopied(taken, unsupported, tag), "warning"
	default:
		noun := "drafts"
		if created == 1 {
			noun = "draft"
		}
		msg, kind = fmt.Sprintf("%d %s created in %s", created, noun, tag), "success"
		if skipped > 0 {
			msg += fmt.Sprintf(", %d skipped (already has a copy, or cannot be copied)", skipped)
		}
		msg += "."
		if failed > 0 {
			msg += fmt.Sprintf(" %d failed", failed)
			if firstErr != "" {
				msg += ": " + firstErr
			} else {
				msg += "."
			}
			kind = "error"
		}
	}
	a.SetFlash(w, r, kind, msg)
	Redirect(w, r, back)
}

// nothingCopied is the flash of a run that made no copy and failed on none:
// taken rows already had a copy in tag, other rows could not be copied.
func nothingCopied(taken, other int, tag string) string {
	switch n := taken + other; {
	case n == 0:
		return "Nothing copied: no pages were selected."
	case other > 0:
		return fmt.Sprintf("Nothing copied: %d already have a %s copy or cannot be copied.", n, tag)
	case taken == 1:
		return "Nothing copied: the page already has a " + tag + " copy."
	default:
		return fmt.Sprintf("Nothing copied: all %d already have a %s copy.", taken, tag)
	}
}
