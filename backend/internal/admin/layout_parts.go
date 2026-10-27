package admin

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/builder"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/siteconfig"
)

// partsListMax bounds the parts offered in total. The entity list serves at
// most partsPage rows per request (a larger limit silently becomes 20), so the
// parts are read page by page.
const (
	partsListMax = 500
	partsPage    = 100
)

// partRow is one layout slot of the Layout parts page.
type partRow struct {
	Slot, Title, Help, Notice string
	Supported                 bool
	Options                   []ui.Option
	EditHref                  string
	Error                     string
}

type partsData struct {
	Rows []partRow
	// Unsupported is one callout above the cards: the parts the live theme does
	// not use. Empty when it uses them all.
	Unsupported string
	Available   bool
	Summary     []ui.FieldError
}

var partSlotInfo = map[string]struct{ title, help string }{
	builder.SlotHeader:  {"Header", "Shown at the top of every page."},
	builder.SlotFooter:  {"Footer", "Shown at the bottom of every page."},
	builder.SlotSingle:  {"Single page layout", "Wraps the content of one page or post. Place the Content slot where it goes."},
	builder.SlotArchive: {"Archive layout", "Wraps the list of posts on archive pages. Place the Content slot where the list goes."},
}

// partsOf lists the theme parts by slot, as {slot: [{id,name}]}.
func (a *Admin) partsOf(r *http.Request) (map[string][]entity.Entity, error) {
	out := map[string][]entity.Entity{}
	sc := a.schemaBySlug(r, entity.PartSchemaSlug)
	if sc == nil || !sc.IsBuiltin {
		return out, nil
	}
	for page := 1; page <= partsListMax/partsPage; page++ {
		res, err := a.deps.Content.Entities.ListEntitiesWithOffset(r.Context(),
			&entity.ListEntitiesFilter{SchemaID: &sc.ID, Sort: "name", Asc: true}, &entity.OffsetPage{Page: page, Limit: partsPage})
		if err != nil || res == nil {
			return out, err
		}
		for _, e := range res.Items {
			var attrs struct {
				Slot string `json:"slot"`
			}
			if json.Unmarshal(e.Attributes, &attrs) == nil && builder.ValidPartSlot(attrs.Slot) {
				out[attrs.Slot] = append(out[attrs.Slot], *e)
			}
		}
		if page >= res.TotalPages {
			break
		}
	}
	return out, nil
}

// partsView builds the page: assigned (slot -> id) is what the select shows,
// errs the per-slot problems of a rejected post.
func (a *Admin) partsView(r *http.Request, assigned map[string]string, errs map[string]string) (partsData, error) {
	var d partsData
	parts, err := a.partsOf(r)
	if err != nil {
		return d, err
	}
	ps := a.schemaBySlug(r, entity.PartSchemaSlug)
	d.Available = ps != nil && ps.IsBuiltin
	var supported []string
	if a.deps.BuilderParts != nil {
		supported = a.deps.BuilderParts()
	}
	var unsupported []string
	for _, slot := range builder.PartSlots() {
		info := partSlotInfo[slot]
		row := partRow{Slot: slot, Title: info.title, Help: info.help, Supported: slices.Contains(supported, slot), Error: errs[slot]}
		if !row.Supported {
			unsupported = append(unsupported, strings.ToLower(info.title))
		}
		cur := assigned[slot]
		row.Options = []ui.Option{{Value: "", Label: "None: use the theme's own", Selected: cur == ""}}
		found := cur == ""
		for _, e := range parts[slot] {
			sel := e.ID.String() == cur
			found = found || sel
			row.Options = append(row.Options, ui.Option{Value: e.ID.String(), Label: e.Name, Selected: sel})
		}
		if !found {
			row.Notice = "The part chosen before no longer exists, so the theme's own is used."
		} else if cur != "" {
			row.EditHref = a.adminPath + "/entities/" + cur + "/edit"
		}
		d.Rows = append(d.Rows, row)
		if row.Error != "" {
			d.Summary = append(d.Summary, ui.FieldError{ID: "part-" + slot, Label: info.title, Message: row.Error})
		}
	}
	if len(unsupported) > 0 {
		d.Unsupported = "Your theme does not use these parts: " + strings.Join(unsupported, ", ") + ". You can build and assign one now; nothing changes until the theme supports it."
		if len(unsupported) == 1 {
			d.Unsupported = "Your theme does not use the " + unsupported[0] + " part. You can build and assign one now; nothing changes until the theme supports it."
		}
	}
	return d, nil
}

func (a *Admin) renderParts(w http.ResponseWriter, r *http.Request, status int, d partsData) {
	v := a.NewView(w, r, "Layout parts")
	v.Section, v.Tab = "parts", "parts"
	v.Data = d
	v.Width = "narrow"
	a.Render(w, r, status, "settings/layoutparts", v)
}

func (a *Admin) handlePartsForm(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	design, _ := builder.ParseDesign(cfg.ThemeExtra)
	d, err := a.partsView(r, design.Parts, nil)
	if err != nil {
		a.serverError(w, r, "The layout parts could not be listed", err)
		return
	}
	a.renderParts(w, r, http.StatusOK, d)
}

// partProblem is why the part id posted for slot cannot be assigned ("" if it can).
func (a *Admin) partProblem(r *http.Request, slot, id string) string {
	uid, err := uuid.Parse(id)
	if err != nil || uid.String() != id { // the stored id is the lower-case form
		return "Choose one of the listed parts."
	}
	e, err := a.deps.Content.Entities.GetEntity(r.Context(), uid)
	if err != nil {
		return "Choose one of the listed parts."
	}
	sc := e.Schema
	if sc == nil {
		if sc, err = a.deps.Content.Entities.GetSchema(r.Context(), e.SchemaID); err != nil {
			return "Choose one of the listed parts."
		}
	}
	var attrs struct {
		Slot string `json:"slot"`
	}
	_ = json.Unmarshal(e.Attributes, &attrs)
	if !entity.IsPartSchema(sc) || attrs.Slot != slot {
		return "That is not a " + slot + " part."
	}
	return ""
}

func (a *Admin) handlePartsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.Error(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	posted, errs := map[string]string{}, map[string]string{}
	for key := range r.PostForm {
		if s, ok := strings.CutPrefix(key, "part_"); ok && !builder.ValidPartSlot(s) {
			errs[s] = "Unknown layout part."
		}
	}
	for _, slot := range builder.PartSlots() {
		id := strings.TrimSpace(r.PostFormValue("part_" + slot))
		posted[slot] = id
		if id == "" {
			continue
		}
		if msg := a.partProblem(r, slot, id); msg != "" {
			errs[slot] = msg
		}
	}
	if len(errs) > 0 {
		d, err := a.partsView(r, posted, errs)
		if err != nil {
			a.serverError(w, r, "The layout parts could not be listed", err)
			return
		}
		if len(d.Summary) == 0 { // an unknown slot has no row: say so on top
			d.Summary = []ui.FieldError{{ID: "part-" + builder.SlotHeader, Label: "Layout parts", Message: "Unknown layout part."}}
		}
		a.renderParts(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	cfg, err := a.loadSite(r.Context())
	if err != nil {
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The settings could not be loaded.")
		return
	}
	raw, err := builder.MergeParts(cfg.ThemeExtra, posted)
	if err == nil {
		_, err = a.deps.Site.Update(r.Context(), siteconfig.UpdateInput{ThemeExtra: raw})
	}
	if err != nil {
		a.logger().Error("save layout parts failed", "err", err)
		a.Error(w, r, http.StatusInternalServerError, "Something went wrong", "The layout parts could not be saved.")
		return
	}
	a.invalidate(r.Context())
	a.SetFlash(w, r, "success", "Layout parts saved.")
	http.Redirect(w, r, a.adminPath+"/theme/parts", http.StatusSeeOther)
}
