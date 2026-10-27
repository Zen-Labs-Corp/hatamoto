package admin

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
)

// nodeVersionScan is how many of the newest saved versions one answer looks through (the most the version list
// returns in one read); nodeVersionCap is how many matching rows it sends back.
const (
	nodeVersionScan = 100
	nodeVersionCap  = 50
)

// nodeVersionRow is one saved version that holds the element. The node comes
// without its children: the restore keeps the ones the page has now.
type nodeVersionRow struct {
	Version int                `json:"version"`
	Saved   time.Time          `json:"saved"`
	By      string             `json:"by"`
	Summary string             `json:"summary"`
	Node    nodeVersionContent `json:"node"`
	Parent  string             `json:"parent"`
	Index   int                `json:"index"`
}

type nodeVersionContent struct {
	T string                       `json:"t"`
	P map[string]any               `json:"p,omitempty"`
	S map[string]map[string]string `json:"s,omitempty"`
	B map[string]string            `json:"b,omitempty"`
}

// handleBuilderNodeVersions lists, newest first, the earlier saved versions of
// a page that hold the element ?node=<id>, with the element as it was and where
// it sat. It only reads: the client turns the choice into ordinary edits.
func (a *Admin) handleBuilderNodeVersions(w http.ResponseWriter, r *http.Request) {
	e, _, ok := a.builderGate(w, r)
	if !ok {
		return
	}
	nodeID := r.URL.Query().Get("node")
	if nodeID == "" {
		a.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Say which element."})
		return
	}
	vs, err := a.deps.Content.Entities.ListVersions(r.Context(), e.ID, nodeVersionScan)
	if err != nil {
		a.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "The versions could not be loaded."})
		return
	}
	names := map[uuid.UUID]string{}
	who := func(u *uuid.UUID) string {
		if u == nil {
			return ""
		}
		if n, ok := names[*u]; ok {
			return n
		}
		n := "Unknown user"
		if a.deps.Users != nil {
			if usr, err := a.deps.Users.GetByID(r.Context(), *u); err == nil {
				n = displayName(usr)
			}
		}
		names[*u] = n
		return n
	}
	rows := []nodeVersionRow{}
	for _, v := range vs {
		if len(rows) == nodeVersionCap {
			break
		}
		d, state := builderDoc(&entity.Entity{Attributes: v.Attributes})
		if state != docOK {
			continue
		}
		n, parent := d.Find(nodeID)
		if n == nil {
			continue
		}
		row := nodeVersionRow{Version: v.Version, Saved: v.CreatedAt, By: who(v.ChangedBy), Node: nodeVersionContent{T: n.T, P: n.P, S: n.S, B: n.B}, Index: indexIn(siblings(d, parent), nodeID)}
		if v.ChangeSummary != nil {
			row.Summary = *v.ChangeSummary
		}
		if parent != nil {
			row.Parent = parent.ID
		}
		rows = append(rows, row)
	}
	w.Header().Set("Cache-Control", "no-store")
	a.writeJSON(w, http.StatusOK, rows)
}
