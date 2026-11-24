package admin

import (
	"context"
	"net/http"
	"time"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/httpx"
	"hatamoto.dev/core/internal/kit"
)

// KitService reads the kits the site and the registry know and applies one (internal/server wires it).
type KitService interface {
	// List returns the applied kits and the kits the registry offers. A non-nil
	// error means only the registry could not be read: applied is still valid.
	List(ctx context.Context) (applied []KitApplied, available []KitEntry, err error)
	// Plan reads what applying the named registry kit would do. Nothing is written.
	Plan(ctx context.Context, name string) (*kit.Plan, error)
	// Apply applies a plan read by Plan.
	Apply(ctx context.Context, pl *kit.Plan, a kit.Accepts, o kit.ApplyOptions) (*kit.Result, error)
}

// KitApplied is one applied kit.
type KitApplied struct {
	Name, Title, Version, AppliedBy string
	AppliedAt                       time.Time
}

// KitEntry is one kit the registry offers.
type KitEntry struct {
	Name, Title, Description, Version, Tier string
	Applied                                 bool
}

// kitsListView is the data behind Settings > Kits.
type kitsListView struct {
	Applied     []KitApplied
	Available   []KitEntry
	RegistryErr bool
}

// mountKits registers Settings > Kits. Applying a kit installs packages and can turn comments on, so it is owner only.
func (a *Admin) mountKits(authed *httpx.Group) {
	owner := authed.Group("", a.RequireCan(authz.ActionKitApply))
	owner.HandleFunc("GET /settings/kits", a.handleKits)
	owner.HandleFunc("GET /settings/kits/{name}", a.handleKitReview)
	owner.HandleFunc("POST /settings/kits/{name}", a.handleKitApply)
}

func (a *Admin) handleKits(w http.ResponseWriter, r *http.Request) {
	svc := a.deps.Kits
	if svc == nil {
		a.unavailable(w, r, "Kits")
		return
	}
	applied, available, err := svc.List(r.Context())
	if err != nil {
		a.logger().Warn("kits list", "err", err)
	}
	a.renderKits(w, r, http.StatusOK, kitsListView{Applied: applied, Available: available, RegistryErr: err != nil})
}

func (a *Admin) renderKits(w http.ResponseWriter, r *http.Request, status int, d kitsListView) {
	v := a.NewView(w, r, "Kits")
	v.Section, v.Tab = "kits", "kits"
	v.Width = "compact"
	v.Data = d
	a.Render(w, r, status, "settings/kits", v)
}
