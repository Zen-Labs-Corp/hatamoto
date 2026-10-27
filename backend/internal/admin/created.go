package admin

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin/ui"
)

// createdID returns ?created= when it is a valid UUID, else "". Anything else
// is ignored so a crafted link never echoes text into the page.
func createdID(r *http.Request) string {
	id, err := uuid.Parse(r.URL.Query().Get("created"))
	if err != nil {
		return ""
	}
	return id.String()
}

// pinFirst moves the row with the given ID to index 0, keeping the order of the
// others. It is a no-op when the ID is empty or not in rows. Callers apply it
// on page 1 only.
func pinFirst(rows []ui.Row, id string) []ui.Row {
	if id == "" {
		return rows
	}
	for i, r := range rows {
		if r.ID != id {
			continue
		}
		out := make([]ui.Row, 0, len(rows))
		out = append(out, r)
		out = append(out, rows[:i]...)
		return append(out, rows[i+1:]...)
	}
	return rows
}

// createdRedirect is the list URL a small create redirects to: base plus
// ?created=id, or &created=id when base already has a query.
func createdRedirect(base, id string) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "created=" + id
}
