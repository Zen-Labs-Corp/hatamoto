package admin

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/entitycsv"
)

// entityExportData is the column chooser of the entity CSV export.
type entityExportData struct {
	Title, Description string
	Type               string
	Back               string
	Action             string
	Columns            []entitycsv.Column
	Statuses           []ui.Option
}

var errChooseColumn = errors.New("choose at least one column")

var exportStatuses = []struct{ Value, Label string }{
	{"", "All statuses"}, {"draft", "Draft"}, {"published", "Published"}, {"scheduled", "Scheduled"}, {"archived", "Archived"},
}

// exportSchema finds the exportable type of the request, answering 404 itself.
func (a *Admin) exportSchema(w http.ResponseWriter, r *http.Request) *entity.EntitySchema {
	if a.deps.Content.Entities == nil {
		a.unavailable(w, r, "Entities")
		return nil
	}
	sc := a.orderSchema(r, r.URL.Query().Get("type"))
	if sc == nil {
		a.notFound(w, r, "Entity type", a.adminPath+"/entities", "Back to entities")
	}
	return sc
}

func (a *Admin) handleEntityExportForm(w http.ResponseWriter, r *http.Request) {
	sc := a.exportSchema(w, r)
	if sc == nil {
		return
	}
	plural := typePluralFor(sc.Slug, sc.Name)
	back := a.adminPath + "/entities?type=" + url.QueryEscape(sc.Slug)
	d := entityExportData{
		Title:       "Export " + plural + " as CSV",
		Description: "Choose the columns. The file opens in a spreadsheet and can be imported again.",
		Type:        sc.Slug, Back: back, Action: a.adminPath + "/entities/export.csv",
		Columns: entitycsv.AvailableColumns(sc.Schema),
	}
	for _, s := range exportStatuses {
		d.Statuses = append(d.Statuses, ui.Option{Value: s.Value, Label: s.Label, Selected: s.Value == ""})
	}
	v := a.NewView(w, r, d.Title)
	v.Data = d
	a.Render(w, r, http.StatusOK, "entities/export", v)
}

// handleEntityExport streams the chosen columns of one type as CSV, a page at
// a time, so a large type never sits in memory.
func (a *Admin) handleEntityExport(w http.ResponseWriter, r *http.Request) {
	sc := a.exportSchema(w, r)
	if sc == nil {
		return
	}
	q := r.URL.Query()
	cols, err := entitycsv.PickColumns(sc.Schema, q["col"])
	if err == nil && len(cols) == 0 {
		err = errChooseColumn
	}
	if err != nil {
		a.ErrorBack(w, r, http.StatusBadRequest, "Choose the columns", sentence(err.Error()),
			a.adminPath+"/entities/export?type="+url.QueryEscape(sc.Slug), "Back to the export")
		return
	}
	filter := &entity.ListEntitiesFilter{SchemaID: &sc.ID, Sort: "created", Asc: true}
	status := q.Get("status")
	if status != "" {
		st := entity.EntityStatus(status)
		if !entity.ValidStatus(st) {
			a.Error(w, r, http.StatusBadRequest, "Unknown status", "That status does not exist.")
			return
		}
		filter.Status = &st
	}

	ents := a.deps.Content.Entities
	parents := map[uuid.UUID]string{}
	opt := entitycsv.ExportOptions{Type: sc.Slug, ParentKey: func(id uuid.UUID) string {
		if k, ok := parents[id]; ok {
			return k
		}
		k := ""
		if p, err := ents.GetEntity(r.Context(), id); err == nil {
			k = entitycsv.ExportKey(p.Metadata, sc.Slug, p.ID)
		}
		parents[id] = k
		return k
	}}

	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+sc.Slug+"-"+time.Now().UTC().Format("20060102")+`.csv"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)

	written, page, truncated := 0, 1, false
	next := func() ([]*entity.Entity, error) {
		res, err := ents.ListEntitiesWithOffset(r.Context(), filter, &entity.OffsetPage{Page: page, Limit: exportPageSize})
		if err != nil {
			return nil, err
		}
		page++
		items := res.Items
		if written >= exportMaxRows {
			truncated = len(items) > 0
			return nil, nil
		}
		if room := exportMaxRows - written; len(items) > room {
			items, truncated = items[:room], true
		}
		written += len(items)
		_ = rc.Flush()
		if r.Context().Err() != nil {
			return nil, nil
		}
		return items, nil
	}
	if err := entitycsv.WriteCSV(w, cols, next, opt); err != nil {
		// The status line is gone; end the file visibly rather than silently short.
		a.logger().Error("export entities failed", "err", err)
		_, _ = w.Write([]byte("# export stopped: the database could not be read\n"))
	} else if truncated {
		_, _ = w.Write([]byte("# export truncated at " + strconv.Itoa(exportMaxRows) + " rows; filter by status to export the rest\n"))
	}
	a.audit(r, "entity.export_csv", sc.Slug, map[string]any{"rows": written, "columns": len(cols), "status": status})
}
