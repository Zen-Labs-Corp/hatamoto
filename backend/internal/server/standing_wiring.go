package server

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/standing"
	"hatamoto.dev/sdk"
)

// standingEngine is the part of the changeset engine the standing gate uses.
// It never publishes with Publish: the gate classifies, then checks the
// policy, publishes and marks the changeset automatic in one transaction.
type standingEngine interface {
	Classify(ctx context.Context, csID int64) error
	PublishUnderPolicy(ctx context.Context, csID int64, p changeset.StandingPolicy, approvalID int64) (changeset.PolicyVerdict, changeset.PublishResult, error)
}

// standingStore is the part of the approval store the gate uses.
type standingStore interface {
	Active(ctx context.Context, plugin, run string) (*standing.Approval, error)
	Approve(ctx context.Context, plugin, run string, p standing.Policy, fp standing.Fingerprint, by string) (standing.Approval, error)
	Waiting(ctx context.Context, plugin string) ([]standing.WaitingRun, error)
	LastVoided(ctx context.Context, plugin, run string) (*standing.Voided, error)
	Void(ctx context.Context, plugin, reason string) (int64, error)
}

// installedPlugin is the plugin as installed right now: its version and the
// manifest as shipped.
type installedPlugin func(name string) (version string, manifest jsontext.Value, ok bool)

// schemaReader returns the current JSON Schema of an entity schema slug.
type schemaReader func(ctx context.Context, slug string) (raw jsontext.Value, ok bool, err error)

// standingService decides whether a finished cron started run publishes
// itself (pluginhost.StandingGate) and serves the owner's approval screens.
// An approval row is created only by Approve, which the admin reaches only
// behind a sudo session; a manifest on its own never enables anything.
type standingService struct {
	store   standingStore
	eng     standingEngine
	plugin  installedPlugin
	schemas schemaReader
	audit   func(ctx context.Context, action, target string, detail map[string]any)
	log     *slog.Logger
}

// ErrNoStandingRule means the plugin declares no standing approval for the run.
var ErrNoStandingRule = errors.New("the plugin declares no automatic updates for this run")

// standingServiceFor builds the service on the server's parts; nil until the
// database, the changeset engine and the entity service exist.
func (s *Server) standingServiceFor() *standingService {
	if s.db == nil || s.changesets == nil || s.entityService == nil {
		return nil
	}
	return &standingService{
		store: &standing.Store{DB: s.db}, eng: s.changesets, audit: s.changesetAudit, log: s.logger,
		plugin: func(name string) (string, jsontext.Value, bool) {
			if s.packages == nil {
				return "", nil, false
			}
			p := s.packages.PluginPackage(name)
			if p == nil {
				return "", nil, false
			}
			return p.Version, p.Manifest, true
		},
		schemas: func(ctx context.Context, slug string) (jsontext.Value, bool, error) {
			sc, err := s.entityService.GetSchemaBySlug(ctx, slug)
			if errors.Is(err, entity.ErrSchemaNotFound) {
				return nil, false, nil
			}
			if err != nil {
				return nil, false, err
			}
			return sc.Schema, true, nil
		},
	}
}

// standingRule is the standing rule a plugin declares for one of its runs.
type standingRule struct {
	Version, Hash string // the release the rule was read from
	Run           string
	Policy        standing.Policy
}

// Policy returns the rule the installed plugin declares, if any.
func (g *standingService) Policy(plugin string) (standingRule, bool) {
	version, raw, ok := g.plugin(plugin)
	if !ok {
		return standingRule{}, false
	}
	hash, err := standing.ManifestHash(raw)
	if err != nil {
		return standingRule{}, false
	}
	var m sdk.Manifest
	if jsonx.Unmarshal(raw, &m, jsonx.Stored) != nil {
		return standingRule{}, false
	}
	i := slices.IndexFunc(m.Runs, func(r sdk.Run) bool { return r.Cron != "" && r.StandingApproval != nil })
	if i < 0 {
		return standingRule{}, false
	}
	r := m.Runs[i]
	max := r.StandingApproval.MaxPerHour
	if max <= 0 || max > sdk.DefaultStandingPerHour {
		max = sdk.DefaultStandingPerHour
	}
	pol := standing.Policy{
		Fields: slices.Clone(r.StandingApproval.Fields), Schemas: slices.Clone(r.StandingApproval.Schemas), MaxPerHour: max,
	}
	if c := r.StandingApproval.Create; c != nil {
		pol.Create = &standing.CreatePolicy{Schemas: slices.Clone(c.Schemas), MaxPerHour: min(c.MaxPerHour, sdk.MaxStandingCreatePerHour)}
	}
	return standingRule{Version: version, Hash: hash, Run: r.Name, Policy: pol}, true
}

// Active returns the plugin's active approval for the run, nil when none.
func (g *standingService) Active(ctx context.Context, plugin, run string) (*standing.Approval, error) {
	a, err := g.store.Active(ctx, plugin, run)
	if errors.Is(err, standing.ErrNoApproval) {
		return nil, nil
	}
	return a, err
}

// Paused says when and why the run's approval ended, nil when it is active
// or never existed.
func (g *standingService) Paused(ctx context.Context, plugin, run string) (*standing.Voided, error) {
	return g.store.LastVoided(ctx, plugin, run)
}

// Waiting lists the runs held back for review.
func (g *standingService) Waiting(ctx context.Context, plugin string) ([]standing.WaitingRun, error) {
	return g.store.Waiting(ctx, plugin)
}

// currentFingerprint is what the plugin and the named schemas look like now.
func (g *standingService) currentFingerprint(ctx context.Context, plugin string, slugs []string) (standing.Fingerprint, error) {
	version, raw, ok := g.plugin(plugin)
	if !ok {
		return standing.Fingerprint{}, fmt.Errorf("plugin %q is not installed", plugin)
	}
	mh, err := standing.ManifestHash(raw)
	if err != nil {
		return standing.Fingerprint{}, fmt.Errorf("failed to hash the manifest: %w", err)
	}
	fp := standing.Fingerprint{Version: version, ManifestHash: mh, SchemaHashes: map[string]string{}}
	for _, slug := range slugs {
		sc, ok, err := g.schemas(ctx, slug)
		if err != nil {
			return fp, fmt.Errorf("failed to read schema %q: %w", slug, err)
		}
		if !ok {
			return fp, fmt.Errorf("schema %q does not exist", slug)
		}
		h, err := standing.SchemaHash(sc)
		if err != nil {
			return fp, fmt.Errorf("failed to hash schema %q: %w", slug, err)
		}
		fp.SchemaHashes[slug] = h
	}
	return fp, nil
}

// Approve records the owner's approval of the rule the plugin declares now.
// The caller passes no policy: it is read from the installed manifest and
// fingerprinted here, so a form can never widen it. The version and manifest
// hash are those the owner was shown: an update in between is refused.
func (g *standingService) Approve(ctx context.Context, plugin, run, by, version, hash string) error {
	rule, ok := g.Policy(plugin)
	if !ok || rule.Run != run {
		return ErrNoStandingRule
	}
	if rule.Version != version || rule.Hash != hash {
		return admin.ErrStandingChanged
	}
	fp, err := g.currentFingerprint(ctx, plugin, rule.Policy.Schemas)
	if err != nil {
		return err
	}
	a, err := g.store.Approve(ctx, plugin, run, rule.Policy, fp, by)
	if err != nil {
		return err
	}
	g.audit(ctx, "standing.approve", "plugin:"+plugin, map[string]any{"run": run, "approval_id": a.ID, "version": fp.Version})
	return nil
}

// TurnOff ends the plugin's approval because the owner asked. It only narrows
// what the plugin may do, so the admin asks for no extra sign in.
func (g *standingService) TurnOff(ctx context.Context, plugin string) error {
	n, err := g.store.Void(ctx, plugin, "turned off")
	if err != nil || n == 0 {
		return err // nothing was on: no audit row for a stale or repeated request
	}
	g.audit(ctx, "standing.void", "plugin:"+plugin, map[string]any{"reason": "turned off"})
	return nil
}

// activeForCron reports whether the cron tick may start the run.
func (g *standingService) activeForCron(ctx context.Context, plugin, run string) (bool, error) {
	a, err := g.Active(ctx, plugin, run)
	return a != nil, err
}

// standingGate adapts the service to pluginhost.StandingGate (its Active
// returns the approval for the admin, the gate's only a yes or no).
type standingGate struct{ *standingService }

// Active is whether the tick may start the run: an approval is in force, it
// still matches the plugin and schemas, and no earlier run of it is waiting
// for review (back pressure: a refused run is never followed by more). An
// approval that no longer matches is voided with a reason, so the tab shows
// the paused state instead of "on" while every run is refused.
func (g standingGate) Active(ctx context.Context, plugin, run string) (bool, error) {
	ok, err := g.activeForCron(ctx, plugin, run)
	if err != nil || !ok {
		return false, err
	}
	a, err := g.standingService.Active(ctx, plugin, run)
	if err != nil || a == nil {
		return false, err
	}
	cur, err := g.currentFingerprint(ctx, plugin, a.Policy.Schemas)
	if err != nil {
		return false, err
	}
	if !a.Matches(cur) {
		reason := "schema changed"
		if a.FP.Version != cur.Version || a.FP.ManifestHash != cur.ManifestHash {
			reason = "plugin updated"
		}
		if _, err := g.store.Void(ctx, plugin, reason); err != nil {
			return false, err
		}
		g.audit(ctx, "standing.void", "plugin:"+plugin, map[string]any{"run": run, "approval_id": a.ID, "reason": reason})
		return false, nil
	}
	waiting, err := g.store.Waiting(ctx, plugin)
	if err != nil {
		return false, err
	}
	for _, w := range waiting {
		if w.Run != run {
			continue
		}
		// A run the gate never handled (a crash after the run finished, a
		// transient error) looks like a refused one: offer it again, so only
		// a changeset the rule really refuses holds the tick back.
		if ok, err := g.publishUnderApproval(ctx, plugin, run, w.ChangesetID, true); err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

func (g standingGate) AutoPublish(ctx context.Context, plugin, run string, csID int64) error {
	return g.autoPublish(ctx, plugin, run, csID)
}

// autoPublish publishes the changeset only when an approval is active, still
// matches the installed plugin and schemas, and the changeset is inside the
// rule. Every refusal returns nil and leaves the changeset staged for review.
func (g *standingService) autoPublish(ctx context.Context, plugin, run string, csID int64) error {
	_, err := g.publishUnderApproval(ctx, plugin, run, csID, false)
	return err
}

// publishUnderApproval is autoPublish reporting whether the changeset was
// published. A quiet call (the tick offering a waiting changeset again) does
// not log or audit a refusal a second time.
func (g *standingService) publishUnderApproval(ctx context.Context, plugin, run string, csID int64, quiet bool) (bool, error) {
	a, err := g.Active(ctx, plugin, run)
	if err != nil || a == nil {
		return false, err
	}
	cur, err := g.currentFingerprint(ctx, plugin, a.Policy.Schemas)
	if err != nil || !a.Matches(cur) {
		if !quiet {
			g.refused(ctx, plugin, run, csID, a.ID, []string{"The plugin or a schema it names changed since you allowed it."})
		}
		return false, nil // fail closed
	}
	// Classify first: the publish transaction rolls back when it refuses, and
	// the classes should stay for the owner's review.
	if err := g.eng.Classify(ctx, csID); err != nil {
		return false, err
	}
	// The create rule comes from what the owner approved, never the live manifest.
	pol := changeset.StandingPolicy{
		Plugin: plugin, Fields: a.Policy.Fields, Schemas: a.Policy.Schemas, MaxPerHour: a.Policy.MaxPerHour,
	}
	if c := a.Policy.Create; c != nil {
		pol.Create = &changeset.CreatePolicy{Schemas: slices.Clone(c.Schemas), MaxPerHour: c.MaxPerHour}
	}
	v, res, err := g.eng.PublishUnderPolicy(ctx, csID, pol, a.ID)
	if err != nil {
		return false, err
	}
	if !v.OK {
		if !quiet {
			g.refused(ctx, plugin, run, csID, a.ID, v.Reasons)
		}
		return false, nil
	}
	g.audit(ctx, "changeset.publish_auto", changesetTarget(csID), map[string]any{
		"plugin": plugin, "run": run, "approval_id": a.ID, "applied": res.Applied,
	})
	return true, nil
}

func (g *standingService) refused(ctx context.Context, plugin, run string, csID, approvalID int64, reasons []string) {
	g.log.Info("automatic publish refused; the changes wait for review",
		slog.String("plugin", plugin), slog.String("run", run), slog.Int64("changeset", csID), slog.Any("reasons", reasons))
	g.audit(ctx, "changeset.publish_auto_refused", changesetTarget(csID), map[string]any{
		"plugin": plugin, "run": run, "approval_id": approvalID, "reasons": reasons,
	})
}

// setupStanding gives the plugin runtime its gate. It runs after the plugin
// runtime and the changeset engine exist.
func (s *Server) setupStanding() {
	if s.plugins == nil {
		return
	}
	if svc := s.standingServiceFor(); svc != nil {
		s.plugins.SetStandingGate(standingGate{svc})
	}
}
