package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/admin/ui"
	"hatamoto.dev/core/internal/httpx"
)

// mountContent registers the entity, schema, taxonomy and media screens.
func (a *Admin) mountContent(authed *httpx.Group) {
	a.mountEntities(authed)
	a.mountSchemas(authed)
	a.mountTaxonomies(authed)
	a.mountMedia(authed)
}

// uuidParam reads a path value as a UUID.
func uuidParam(r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue(name))
	return id, err == nil
}

// actorID is the signed-in user as the services want it.
func actorID(r *http.Request) *uuid.UUID {
	if u := userFrom(r.Context()); u != nil {
		if id, err := uuid.Parse(u.ID); err == nil {
			return &id
		}
	}
	return nil
}

// notFound is the shared 404 of the content screens.
func (a *Admin) notFound(w http.ResponseWriter, r *http.Request, what, back, backLabel string) {
	a.ErrorBack(w, r, http.StatusNotFound, what+" not found", "That "+strings.ToLower(what)+" does not exist or was deleted.", back, backLabel)
}

// serverError logs err and shows the generic failure page.
func (a *Admin) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	a.logger().Error(what, "err", err)
	a.Error(w, r, http.StatusInternalServerError, "Something went wrong", sentence(what))
}

// siteLocation is the site's time zone (UTC when unset or unknown): schedules
// are typed and shown in it.
func (a *Admin) siteLocation(ctx context.Context) *time.Location {
	if a.deps.Site != nil {
		if cfg, err := a.deps.Site.Get(ctx); err == nil && cfg != nil && cfg.Timezone != "" {
			if loc, err := time.LoadLocation(cfg.Timezone); err == nil {
				return loc
			}
		}
	}
	return time.UTC
}

// backTo returns a same-area path to go back to after an action, from the
// "back" form field or query value, restricted to prefix.
func backTo(r *http.Request, prefix, fallback string) string {
	b := r.FormValue("back")
	if strings.HasPrefix(b, prefix) && !strings.HasPrefix(b, prefix+"/..") && !strings.ContainsAny(b, "\r\n\\") && !strings.HasPrefix(b, "//") {
		return b
	}
	return fallback
}

func sortStrings(s []string) { sort.Strings(s) }

// crumbList builds breadcrumbs from label, href pairs; a final label without
// an href is the current page.
func crumbList(parts ...string) []ui.Crumb {
	var out []ui.Crumb
	for i := 0; i < len(parts); i += 2 {
		c := ui.Crumb{Label: parts[i]}
		if i+1 < len(parts) {
			c.Href = parts[i+1]
		}
		out = append(out, c)
	}
	return out
}

// jsonString quotes s as a JSON string (for HX-Trigger payloads).
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
