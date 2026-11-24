package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/kit"
	"hatamoto.dev/core/internal/packages"
)

// kitRegistryTimeout bounds every registry read on an admin page.
const kitRegistryTimeout = 3 * time.Second

// kitsAdmin adapts internal/kit to the admin's KitService. It holds no kit logic.
type kitsAdmin struct {
	deps    kit.Deps
	applied interface {
		List(ctx context.Context) ([]kit.Applied, error)
	}
	// verify is Manager.VerifyKit.
	verify func(ctx context.Context, raw []byte, src packages.Source) (*packages.Package, packages.Origin, error)
}

// newKitsAdmin wires the kit service from the server's services.
func (s *Server) newKitsAdmin() *kitsAdmin {
	deps := kit.Wire(kit.Services{
		Manager: s.packages, Entities: s.entityService, Menus: s.menuService, Site: s.siteConfig,
		Comments: s.comments, Pool: s.db.Pool(), Address: s.kitAddress, Log: s.logger,
	})
	return &kitsAdmin{deps: deps, applied: kit.AppliedStore{Pool: s.db.Pool()}, verify: s.packages.VerifyKit}
}

// kitAddress is the site address the owner or operator set ("" when none): comments need it.
func (s *Server) kitAddress(ctx context.Context) string {
	if s.cfg != nil {
		if b := strings.TrimRight(strings.TrimSpace(s.cfg.PublicSite.BaseURL), "/"); b != "" {
			return b
		}
	}
	if s.siteConfig != nil {
		if cfg, err := s.siteConfig.Get(ctx); err == nil {
			return strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
		}
	}
	return ""
}

// List returns the applied kits and the registry's kits. A registry that cannot be read within
// 3 seconds (or is switched off) is the returned error; the applied kits are still listed.
func (k *kitsAdmin) List(ctx context.Context) ([]admin.KitApplied, []admin.KitEntry, error) {
	rows, err := k.applied.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, kitRegistryTimeout)
	defer cancel()
	entries, regErr := kit.Available(ctx, k.deps)
	titles := map[string]string{}
	for _, e := range entries {
		titles[e.Name] = e.Title
	}
	applied := make([]admin.KitApplied, len(rows))
	done := map[string]bool{}
	for i, r := range rows {
		applied[i] = admin.KitApplied{Name: r.Name, Title: titles[r.Name], Version: r.Version, AppliedBy: r.AppliedBy, AppliedAt: r.AppliedAt}
		done[r.Name] = true
	}
	if regErr != nil {
		return applied, nil, regErr
	}
	available := make([]admin.KitEntry, len(entries))
	for i, e := range entries {
		available[i] = admin.KitEntry{Name: e.Name, Title: e.Title, Description: e.Description, Version: e.Version, Tier: kitTier(e.Tier, packages.OriginSelfBuilt, ""), Applied: done[e.Name]}
	}
	return applied, available, nil
}

// Plan fetches the kit from the registry, checks its signature and trust, and plans it.
func (k *kitsAdmin) Plan(ctx context.Context, name string) (*kit.Plan, error) {
	ctx, cancel := context.WithTimeout(ctx, kitRegistryTimeout)
	defer cancel()
	idx, err := k.deps.Packages.Registry(ctx)
	if err != nil {
		return nil, err
	}
	e, ok := idx.Find(packages.KindKit, name)
	if !ok {
		return nil, fmt.Errorf("the registry has no kit %q", name)
	}
	raw, err := k.deps.Packages.Fetch(ctx, packages.KindKit, name)
	if err != nil {
		return nil, err
	}
	p, origin, err := k.verify(ctx, raw, packages.SourceRegistry)
	if err != nil {
		return nil, err
	}
	signer := ""
	if p.Signature != nil {
		signer = p.Signature.Signer
	}
	return kit.BuildPlan(ctx, k.deps, p, kit.Options{Origin: origin, Tier: kitTier(e.Tier, origin, signer)})
}

// Apply plans the kit again, so what is applied is what the registry serves now, and applies it.
func (k *kitsAdmin) Apply(ctx context.Context, name string, a kit.Accepts, o kit.ApplyOptions) (*kit.Result, error) {
	pl, err := k.Plan(ctx, name)
	if err != nil {
		return nil, err
	}
	return kit.Apply(actorCtx(ctx), k.deps, pl, a, o)
}

// kitTier is the signer label (Decision 16): the registry's tier when it lists one, else
// first-party for a first-party key, developer for a developer grant, community otherwise.
func kitTier(entryTier string, origin packages.Origin, signer string) string {
	switch {
	case entryTier != "":
		return entryTier
	case origin == packages.OriginDeveloper:
		return packages.TierDeveloper
	case packages.IsFirstPartyKey(signer):
		return packages.TierFirstParty
	}
	return packages.TierCommunity
}

var _ admin.KitService = (*kitsAdmin)(nil)
