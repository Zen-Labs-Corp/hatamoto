package admin

import (
	"fmt"
	"net/http"
	"strconv"

	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
)

// ownershipPanel is the "who owns this page" card of the entity editors.
type ownershipPanel struct {
	CardID      string // the card's id; the second card's is CardID + "-never"
	Title       string
	BadgeLabel  string
	BadgeKind   string // "warning" while a plugin tracks the page, "info" once it is yours
	Sentence    string
	SourceKey   string // what the plugin calls the page ("" when unknown)
	LastLabel   string // the last import, "Changeset #42, 2 Nov 2026" ("" when none)
	LastHref    string // the changeset page ("" unless the reader may open it)
	ActionURL   string // where the hand-back posts
	FormID      string // the one-button form that posts it; the button sits in the card foot
	ButtonLabel string // empty while a plugin tracks the page: there is nothing to hand back
}

// ownershipFormID is the id of the hand-back form the editors render after
// their own form, so the card's button can submit it without nesting forms.
const ownershipFormID = "hm-ownform"

// ownershipPanel builds the card for e, or nil when there is nothing to show:
// no ownership reader is wired, the entity is new, or no plugin ever imported
// it. A failed lookup hides the card rather than the editor.
func (a *Admin) ownershipPanel(r *http.Request, e *entity.Entity) *ownershipPanel {
	own := a.deps.Content.Ownership
	if own == nil || e == nil {
		return nil
	}
	o, err := own.Get(r.Context(), e.ID)
	if err != nil {
		a.logger().Error("ownership lookup failed", "entity", e.ID, "err", err)
		return nil
	}
	if o == nil || o.State == changeset.OwnershipNone {
		return nil
	}
	label := a.pluginLabel(o.Plugin)
	p := &ownershipPanel{
		CardID:    "own",
		Title:     "Who owns this page",
		SourceKey: o.SourceKey,
		ActionURL: a.adminPath + "/entities/" + e.ID.String() + "/origin",
		FormID:    ownershipFormID,
	}
	zone := a.siteLocation(r.Context())
	switch o.State {
	case changeset.OwnershipTracked:
		p.BadgeLabel, p.BadgeKind = "Imported, tracked by "+label, "warning"
		p.Sentence = fmt.Sprintf("A later run of %s can update this page. Editing its text, link, parent, template or SEO makes it yours.", label)
	default:
		p.BadgeLabel, p.BadgeKind = "Yours", "info"
		p.Sentence = fmt.Sprintf("You edited this page on %s, so imports no longer change it.", fmtDate(e.UpdatedAt.In(zone)))
		p.ButtonLabel = "Let " + label + " track this again"
	}
	if o.ChangesetID > 0 {
		p.LastLabel = "Changeset #" + strconv.FormatInt(o.ChangesetID, 10)
		if o.PublishedAt != nil {
			p.LastLabel += ", " + fmtDate(o.PublishedAt.In(zone))
		}
		// The changeset pages are the owner's.
		if u := userFrom(r.Context()); u != nil && u.Role == "owner" {
			p.LastHref = a.changesetURL(o.Plugin, o.ChangesetID, "")
		}
	}
	return p
}

// pluginLabel is the name people know a plugin by: the title of its first
// declared run when the plugin runtime knows it, else its machine name.
func (a *Admin) pluginLabel(name string) string {
	if pa := a.deps.Growth.Plugins; pa != nil {
		for _, pl := range pa.Plugins() {
			if pl.Name == name && len(pl.Runs) > 0 && pl.Runs[0].Title != "" {
				return pl.Runs[0].Title
			}
		}
	}
	return name
}
