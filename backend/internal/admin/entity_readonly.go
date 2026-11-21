package admin

import (
	"net/http"
	"sort"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
)

// readOnlyData feeds entities/readonly: an entity the signed-in user may read but not change.
type readOnlyData struct {
	Name, SchemaName, Status string
	BannerTitle, BannerBody  string
	Tabs                     []ui.Tab
	Fields                   []diffRow // Left holds the value
}

// writerName is the display name of the user who wrote e, never the email. It is "another user" when the
// creator is unknown or has no name.
func (a *Admin) writerName(r *http.Request, e *entity.Entity) string {
	const unknown = "another user"
	if e.CreatedBy == nil || a.deps.Users == nil {
		return unknown
	}
	u, err := a.deps.Users.GetByID(r.Context(), *e.CreatedBy)
	if err != nil || u == nil || u.Name == nil || *u.Name == "" {
		return unknown
	}
	return *u.Name
}

// isEntityCreator reports whether the signed-in user created e.
func isEntityCreator(r *http.Request, e *entity.Entity) bool {
	u := userFrom(r.Context())
	if u == nil || e.CreatedBy == nil {
		return false
	}
	id, err := uuid.Parse(u.ID)
	return err == nil && id == *e.CreatedBy
}

// renderEntityReadOnly shows e without save controls and says who wrote it.
func (a *Admin) renderEntityReadOnly(w http.ResponseWriter, r *http.Request, e *entity.Entity, sc *entity.EntitySchema) {
	d := readOnlyData{Name: e.Name, Status: string(e.Status), Tabs: a.entityTabsFor(r.Context(), e, "details")}
	if sc != nil {
		d.SchemaName = sc.Name
	}
	if isEntityCreator(r, e) {
		d.BannerTitle = "You cannot change this entry"
		d.BannerBody = "It is no longer a draft. An editor or reviewer can change it."
	} else {
		d.BannerTitle = "Written by " + a.writerName(r, e)
		d.BannerBody = "Authors edit their own content."
	}
	var doc any
	_ = jsonx.Unmarshal(e.Attributes, &doc, jsonx.StoredLoose)
	flat := map[string]string{}
	flatten("", doc, flat)
	paths := make([]string, 0, len(flat))
	for p := range flat {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		d.Fields = append(d.Fields, diffRow{Path: p, Left: clipValue(flat[p])})
	}
	v := a.NewView(w, r, e.Name)
	v.Data = d
	v.Crumbs = a.entityCrumbs(e.Name, e.ID.String())
	a.Render(w, r, http.StatusOK, "entities/readonly", v)
}
