package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/core/internal/siteconfig"
	"hatamoto.dev/sdk"
)

// reviewRiskyLimit is how many risky rows one page of the review lists; the
// counts are for the whole changeset.
const reviewRiskyLimit = 200

// settingsStore is the site settings service the Settings > General page
// saves through (siteconfig.Store).
type settingsStore interface {
	Get(ctx context.Context) (*siteconfig.Config, error)
	Update(ctx context.Context, in siteconfig.UpdateInput) (*siteconfig.Config, error)
}

// changesetReviewer is the admin's review service: the changeset engine for
// the items, the run store for the plugin's suggestions, the site settings
// service to apply an approved one. It implements admin.ChangesetReviewer and
// admin.RunReader.
type changesetReviewer struct {
	eng        *changeset.Engine
	runs       *plugrun.Store
	settings   settingsStore
	invalidate func(context.Context) // after the site settings changed
	audit      func(ctx context.Context, action, target string, detail map[string]any)
}

var (
	_ admin.ChangesetReviewer = (*changesetReviewer)(nil)
	_ admin.RunReader         = (*changesetReviewer)(nil)
)

// setting is one site setting a suggestion can change: how to read it from the
// config and how to write it back.
type setting struct {
	key  string
	get  func(*siteconfig.Config) string
	set  func(*siteconfig.UpdateInput, string)
	name string // label on the review page
}

// reviewSettings are the settings a site_settings suggestion may change, in
// the order the page shows them. Language is not here: changing it rewrites
// the locale set, which the Settings page does with its own checks.
var reviewSettings = []setting{
	{"title", func(c *siteconfig.Config) string { return c.CompanyName }, func(u *siteconfig.UpdateInput, v string) { u.CompanyName = &v }, "Site title"},
	{"tagline", func(c *siteconfig.Config) string { return c.Tagline }, func(u *siteconfig.UpdateInput, v string) { u.Tagline = &v }, "Tagline"},
	{"time_zone", func(c *siteconfig.Config) string { return c.Timezone }, func(u *siteconfig.UpdateInput, v string) { u.Timezone = &v }, "Time zone"},
}

func settingByKey(key string) (setting, bool) {
	i := slices.IndexFunc(reviewSettings, func(s setting) bool { return s.key == key })
	if i < 0 {
		return setting{}, false
	}
	return reviewSettings[i], true
}

// suggestionValues reads a site_settings proposal. ok is false when it holds
// anything the review page cannot apply.
func suggestionValues(p plugrun.Proposal) (map[string]string, bool) {
	if p.Kind != sdk.ProposeSiteSettings {
		return nil, false
	}
	var m map[string]string
	if json.Unmarshal(p.Payload, &m) != nil || len(m) == 0 {
		return nil, false
	}
	for k := range m {
		if _, ok := settingByKey(k); !ok {
			return m, false
		}
	}
	return m, true
}

// applySettings writes values (keyed like a proposal) through the settings
// service and refreshes what depends on them.
func (r *changesetReviewer) applySettings(ctx context.Context, values map[string]string) error {
	var in siteconfig.UpdateInput
	for k, v := range values {
		s, ok := settingByKey(k)
		if !ok {
			return fmt.Errorf("%w: %q", admin.ErrReviewNotApplicable, k)
		}
		s.set(&in, v)
	}
	if _, err := r.settings.Update(ctx, in); err != nil {
		return fmt.Errorf("failed to apply settings: %w", err)
	}
	if r.invalidate != nil {
		r.invalidate(ctx)
	}
	return nil
}

func reviewErr(err error) error {
	switch {
	case errors.Is(err, changeset.ErrNotFound):
		return admin.ErrReviewNotFound
	case errors.Is(err, changeset.ErrNotStaged):
		return admin.ErrReviewNotStaged
	case errors.Is(err, changeset.ErrExpired):
		return admin.ErrReviewExpired
	case errors.Is(err, changeset.ErrNotPublished):
		return admin.ErrReviewNotPublished
	case errors.Is(err, changeset.ErrUndecided):
		return admin.ErrReviewUndecided
	}
	return err
}

var kindLabels = map[changeset.Kind]string{
	changeset.KindEntity: "Pages and entries", changeset.KindTerm: "Terms", changeset.KindMenu: "Menus",
	changeset.KindRedirect: "Redirects", changeset.KindMedia: "Media",
}

func (r *changesetReviewer) Review(ctx context.Context, id int64, page int) (admin.ReviewData, error) {
	h, err := r.eng.Header(ctx, id)
	if err != nil {
		return admin.ReviewData{}, reviewErr(err)
	}
	if err := r.eng.Classify(ctx, id); err != nil {
		return admin.ReviewData{}, reviewErr(err)
	}
	counts, err := r.eng.ReviewCounts(ctx, id)
	if err != nil {
		return admin.ReviewData{}, reviewErr(err)
	}
	if err := r.runFinished(ctx, h); err != nil {
		return admin.ReviewData{}, err
	}
	// Count the items themselves, in one pass with the risk and drop counts. The
	// header's item_count is kept in step with every insert and still feeds the
	// quota, the lists and the run page.
	items := counts.Low + counts.High + counts.Critical
	d := admin.ReviewData{
		ID: id, Plugin: h.Plugin, State: h.State, ExpiresAt: h.ExpiresAt, Items: items,
		Low: counts.Low, High: counts.High, Critical: counts.Critical, HighDecided: counts.HighDecided,
		DroppedCount: counts.Dropped, PublishCount: max(items-counts.Dropped, 0), PayloadBytes: counts.PayloadBytes,
		Previewable: h.State == changeset.StateStaged,
	}
	run, err := r.runs.Get(ctx, h.RunID)
	if err != nil {
		return d, fmt.Errorf("failed to read run: %w", err)
	}
	d.RunName = run.Name
	for _, k := range counts.ByKind {
		d.Kinds = append(d.Kinds, admin.KindRow{Label: kindLabels[k.Kind], New: k.New, Updated: k.Updated, Removed: k.Removed})
	}
	if err := r.reviewRisks(ctx, id, max(page, 1), &d); err != nil {
		return d, err
	}
	if d.Pending, err = r.pendingItems(ctx, id); err != nil {
		return d, err
	}
	if h.State == changeset.StateStaged {
		stale, err := r.eng.StaleSeqs(ctx, id)
		if err != nil {
			return d, reviewErr(err)
		}
		for _, it := range stale {
			d.Skipped = append(d.Skipped, admin.SkippedRow{Label: staleLabel(it), Why: "changed on the site since the plugin read it"})
		}
	}
	props, err := r.runs.Proposals(ctx, h.RunID)
	if err != nil {
		return d, fmt.Errorf("failed to read suggestions: %w", err)
	}
	undecided := 0
	for _, p := range props {
		d.Suggestions = append(d.Suggestions, r.suggestion(ctx, p))
		if p.Decision == "" {
			undecided++
		}
	}
	d.CanPublish = h.State == changeset.StateStaged && d.Pending == 0 && undecided == 0
	return d, nil
}

func staleLabel(it changeset.ItemSummary) string {
	if it.Label != "" {
		return it.Label
	}
	return it.SourceKey
}

// reviewRisks fills one page of the risky rows and the redirect group.
// Redirects that point at a staged page are decided with that page, so they
// are one group (shown on the first page), not rows.
func (r *changesetReviewer) reviewRisks(ctx context.Context, id int64, page int, d *admin.ReviewData) error {
	items, err := r.eng.ReviewRiskyItems(ctx, id, reviewRiskyLimit, (page-1)*reviewRiskyLimit)
	if err != nil {
		return reviewErr(err)
	}
	for _, it := range items {
		d.Risky = append(d.Risky, admin.ReviewItem{
			Seq: it.Seq, Kind: string(it.Kind), Op: it.Op, Label: it.Label, Risk: it.Risk,
			Reason: it.Reason, Decision: it.Decision, Old: it.Old, New: it.New,
		})
	}
	d.RiskyPage, d.RiskyPerPage = page, reviewRiskyLimit
	rows, err := r.eng.DB.Pool().Query(ctx, `
		SELECT seq, COALESCE(decision, '') FROM hatamoto_changeset_items
		WHERE changeset_id = $1 AND risk_reason = 'redirect' AND COALESCE(risk_ref, 0) > 0 ORDER BY seq`, id)
	if err != nil {
		return fmt.Errorf("failed to read redirect group: %w", err)
	}
	defer rows.Close()
	var g admin.RedirectGroup
	same := true
	for rows.Next() {
		var seq int
		var dec string
		if err := rows.Scan(&seq, &dec); err != nil {
			return err
		}
		if len(g.Seqs) == 0 {
			g.Decision = dec
		} else if dec != g.Decision {
			same = false
		}
		g.Seqs = append(g.Seqs, seq)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(g.Seqs) > 0 {
		if !same {
			g.Decision = ""
		}
		g.Count = len(g.Seqs)
		d.RedirectGroup = &g
	}
	d.RiskyTotal = max(d.High+d.Critical-len(g.Seqs), 0)
	return nil
}

// pendingItems counts the high and critical items that still need a decision
// before a publish: not decided, and not dropped along with their page.
func (r *changesetReviewer) pendingItems(ctx context.Context, id int64) (int, error) {
	var n int
	err := r.eng.DB.Pool().QueryRow(ctx, `
		SELECT count(*) FROM hatamoto_changeset_items i
		WHERE i.changeset_id = $1 AND i.risk IN ('high', 'critical') AND i.decision IS NULL
		  AND NOT (i.risk_reason = 'redirect' AND COALESCE(i.risk_ref, 0) > 0 AND EXISTS (
		        SELECT 1 FROM hatamoto_changeset_items p
		        WHERE p.changeset_id = i.changeset_id AND p.seq = i.risk_ref AND p.decision = 'dropped'))`, id).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("failed to count undecided items: %w", err)
	}
	return n, nil
}

// suggestion maps a stored proposal; Old is the setting as it is now.
func (r *changesetReviewer) suggestion(ctx context.Context, p plugrun.Proposal) admin.Suggestion {
	s := admin.Suggestion{ID: p.ID, Kind: p.Kind, Title: suggestionTitle(p.Kind), Decision: p.Decision}
	values, ok := suggestionValues(p)
	s.Applicable = ok
	if p.Kind != sdk.ProposeSiteSettings {
		return s
	}
	cfg, _ := r.settings.Get(ctx)
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return settingOrder(keys[i]) < settingOrder(keys[j]) })
	for _, k := range keys {
		f := admin.SuggestionField{Name: k, New: values[k]}
		if def, ok := settingByKey(k); ok {
			f.Name = def.name
			if cfg != nil {
				f.Old = def.get(cfg)
			}
		}
		s.Fields = append(s.Fields, f)
	}
	return s
}

func settingOrder(key string) int {
	i := slices.IndexFunc(reviewSettings, func(s setting) bool { return s.key == key })
	if i < 0 {
		return len(reviewSettings)
	}
	return i
}

func suggestionTitle(kind string) string {
	switch kind {
	case sdk.ProposeSiteSettings:
		return "Change site settings"
	case sdk.ProposeURLPatterns:
		return "Change URL patterns"
	case sdk.ProposeHomePage:
		return "Change the home page"
	case sdk.ProposeMenuLocation:
		return "Change a menu location"
	}
	return strings.ReplaceAll(kind, "_", " ")
}

func (r *changesetReviewer) Decide(ctx context.Context, id int64, seqs []int, decision string) error {
	switch decision {
	case "approved":
		decision = changeset.DecisionApproved
	case "dropped":
		decision = changeset.DecisionDropped
	case "":
	default:
		return fmt.Errorf("unknown decision %q", decision)
	}
	if _, err := r.stagedHeader(ctx, id); err != nil {
		return err
	}
	if err := r.eng.SetDecisions(ctx, id, seqs, decision); err != nil {
		return reviewErr(err)
	}
	r.audit(ctx, "changeset.decide", changesetTarget(id), map[string]any{"seqs": len(seqs), "decision": decision})
	return nil
}

func changesetTarget(id int64) string { return fmt.Sprintf("changeset:%d", id) }

func (r *changesetReviewer) stagedHeader(ctx context.Context, id int64) (changeset.Header, error) {
	h, err := r.eng.Header(ctx, id)
	if err != nil {
		return h, reviewErr(err)
	}
	if h.State != changeset.StateStaged {
		return h, admin.ErrReviewNotStaged
	}
	return h, r.runFinished(ctx, h)
}

// runFinished refuses to review a changeset whose run is still staging, or
// ended without finishing: the owner would sign off a set that is incomplete.
func (r *changesetReviewer) runFinished(ctx context.Context, h changeset.Header) error {
	if h.State != changeset.StateStaged {
		return nil
	}
	run, err := r.runs.Get(ctx, h.RunID)
	if err != nil {
		return fmt.Errorf("failed to read run: %w", err)
	}
	if run.State != plugrun.StateFinished {
		return admin.ErrReviewRunning
	}
	return nil
}

// suggestionErr maps the run store's refusal to the admin's.
func suggestionErr(err error) error {
	if errors.Is(err, plugrun.ErrProposalDecided) {
		return admin.ErrReviewSuggestionDecided
	}
	return err
}

func (r *changesetReviewer) DecideSuggestion(ctx context.Context, id, proposalID int64, decision string) error {
	return suggestionErr(r.decideSuggestion(ctx, id, proposalID, decision))
}

func (r *changesetReviewer) decideSuggestion(ctx context.Context, id, proposalID int64, decision string) error {
	h, err := r.stagedHeader(ctx, id)
	if err != nil {
		return err
	}
	props, err := r.runs.Proposals(ctx, h.RunID)
	if err != nil {
		return fmt.Errorf("failed to read suggestions: %w", err)
	}
	i := slices.IndexFunc(props, func(p plugrun.Proposal) bool { return p.ID == proposalID })
	if i < 0 {
		return admin.ErrReviewNotFound
	}
	p := props[i]
	if p.Decision != "" {
		return admin.ErrReviewSuggestionDecided
	}
	switch decision {
	case plugrun.ProposalRejected:
		if err := r.runs.SetProposalDecision(ctx, h.RunID, proposalID, decision, nil); err != nil {
			return err
		}
	case plugrun.ProposalApproved:
		if err := r.approve(ctx, h.RunID, p); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown decision %q", decision)
	}
	r.audit(ctx, "changeset.suggestion."+decision, changesetTarget(id), map[string]any{"proposal": proposalID, "kind": p.Kind})
	return nil
}

// approve records the owner's approval of a site_settings suggestion. The
// settings change at publish time (applyApproved), so a discarded or expired
// changeset leaves them alone.
func (r *changesetReviewer) approve(ctx context.Context, runID uuid.UUID, p plugrun.Proposal) error {
	if _, ok := suggestionValues(p); !ok {
		return admin.ErrReviewNotApplicable
	}
	return r.runs.SetProposalDecision(ctx, runID, p.ID, plugrun.ProposalApproved, nil)
}

// applyApproved writes the approved suggestions that are not applied yet, each
// with the values it replaced so Undo can put them back.
func (r *changesetReviewer) applyApproved(ctx context.Context, runID uuid.UUID) error {
	props, err := r.runs.Proposals(ctx, runID)
	if err != nil {
		return fmt.Errorf("failed to read suggestions: %w", err)
	}
	for _, p := range props {
		if p.Decision != plugrun.ProposalApproved || len(p.Before) > 0 {
			continue
		}
		values, ok := suggestionValues(p)
		if !ok {
			return admin.ErrReviewNotApplicable
		}
		cfg, err := r.settings.Get(ctx)
		if err != nil {
			return fmt.Errorf("failed to read settings: %w", err)
		}
		before := make(map[string]string, len(values))
		for k := range values {
			def, _ := settingByKey(k)
			before[k] = def.get(cfg)
		}
		raw, err := json.Marshal(before)
		if err != nil {
			return err
		}
		if err := r.runs.SetProposalBefore(ctx, runID, p.ID, raw); err != nil {
			return err
		}
		if err := r.applySettings(ctx, values); err != nil {
			return err
		}
	}
	return nil
}

func (r *changesetReviewer) Publish(ctx context.Context, id int64) (applied, dropped int, err error) {
	h, err := r.stagedHeader(ctx, id)
	if err != nil {
		return 0, 0, err
	}
	undecided, err := r.runs.UndecidedProposals(ctx, h.RunID)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to count suggestions: %w", err)
	}
	if undecided > 0 {
		return 0, 0, admin.ErrReviewUndecided
	}
	// PublishReviewed classifies inside its transaction and rolls that back
	// when it refuses; classify first so the refusal leaves the risks saved.
	if err := r.eng.Classify(ctx, id); err != nil {
		return 0, 0, reviewErr(err)
	}
	res, err := r.eng.PublishReviewed(ctx, id)
	if err != nil {
		return 0, 0, reviewErr(err)
	}
	if err := r.applyApproved(ctx, h.RunID); err != nil {
		return res.Applied, res.Dropped, fmt.Errorf("published, but the approved suggestions could not be applied: %w", err)
	}
	r.audit(ctx, "changeset.publish", changesetTarget(id), map[string]any{"applied": res.Applied, "skipped": res.Skipped, "dropped": res.Dropped})
	return res.Applied, res.Dropped, nil
}

func (r *changesetReviewer) Discard(ctx context.Context, id int64) error {
	if err := r.eng.Discard(ctx, id); err != nil {
		return reviewErr(err)
	}
	r.audit(ctx, "changeset.discard", changesetTarget(id), nil)
	return nil
}

func (r *changesetReviewer) History(ctx context.Context, limit, offset int) ([]admin.HistoryRow, error) {
	rows, err := r.eng.List(ctx, "", limit, offset)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(rows))
	for i, h := range rows {
		ids[i] = h.ID
	}
	by, err := r.publishers(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]admin.HistoryRow, 0, len(rows))
	for _, h := range rows {
		when := h.ExpiresAt.Add(-changeset.TTL) // created
		switch {
		case h.RevertedAt != nil:
			when = *h.RevertedAt
		case h.PublishedAt != nil:
			when = *h.PublishedAt
		}
		out = append(out, admin.HistoryRow{ID: h.ID, Plugin: h.Plugin, RunName: h.RunName, State: h.State, By: by[h.ID], Auto: h.Auto,
			Building: h.State == changeset.StateStaged && h.RunState != plugrun.StateFinished,
			When:     when, Applied: h.Applied, Skipped: h.Skipped, Dropped: h.Dropped})
	}
	return out, nil
}

// publishers names who published each of the changesets: the signed-in user
// of the newest publish in the audit log, by name or else by email. A
// changeset that was never published, or whose publisher is gone, is absent.
func (r *changesetReviewer) publishers(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 || r.runs == nil || r.runs.DB == nil {
		return out, nil
	}
	rows, err := r.runs.DB.Pool().Query(ctx, `
		SELECT DISTINCT ON (t.id) t.id, coalesce(nullif(u.name, ''), u.email)
		FROM unnest($1::bigint[]) AS t(id)
		JOIN hatamoto_admin_audit a ON a.action = 'changeset.publish' AND a.target IN (t.id::text, 'changeset:' || t.id)
		JOIN hatamoto_users u ON u.id = a.actor_id
		ORDER BY t.id, a.at DESC, a.id DESC`, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to read who published the changesets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var who string
		if err := rows.Scan(&id, &who); err != nil {
			return nil, fmt.Errorf("failed to read who published a changeset: %w", err)
		}
		out[id] = who
	}
	return out, rows.Err()
}

// appliedSuggestions are the approved suggestions of a changeset's run that
// still hold a pre-image to restore.
func (r *changesetReviewer) appliedSuggestions(ctx context.Context, runID uuid.UUID) ([]plugrun.Proposal, error) {
	props, err := r.runs.Proposals(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to read suggestions: %w", err)
	}
	return slices.DeleteFunc(props, func(p plugrun.Proposal) bool { return p.Decision != plugrun.ProposalApproved || len(p.Before) == 0 }), nil
}

func (r *changesetReviewer) UndoPreview(ctx context.Context, id int64) (admin.UndoPreview, error) {
	h, err := r.eng.Header(ctx, id)
	if err != nil {
		return admin.UndoPreview{}, reviewErr(err)
	}
	moved, total, err := r.eng.RevertPreview(ctx, id)
	if err != nil {
		return admin.UndoPreview{}, reviewErr(err)
	}
	applied, err := r.appliedSuggestions(ctx, h.RunID)
	if err != nil {
		return admin.UndoPreview{}, err
	}
	return admin.UndoPreview{Applied: total, EditedSince: moved, Settings: len(applied)}, nil
}

func (r *changesetReviewer) Undo(ctx context.Context, id int64) (restored, keptEdited int, err error) {
	h, err := r.eng.Header(ctx, id)
	if err != nil {
		return 0, 0, reviewErr(err)
	}
	res, err := r.eng.Revert(ctx, id)
	if err != nil {
		return 0, 0, reviewErr(err)
	}
	applied, err := r.appliedSuggestions(ctx, h.RunID)
	if err != nil {
		return res.Applied, res.Skipped, err
	}
	for _, p := range applied {
		var before map[string]string
		if err := json.Unmarshal(p.Before, &before); err != nil {
			return res.Applied, res.Skipped, fmt.Errorf("suggestion %d: unreadable pre-image: %w", p.ID, err)
		}
		if err := r.applySettings(ctx, before); err != nil {
			return res.Applied, res.Skipped, err
		}
		if err := r.runs.ClearProposalDecision(ctx, h.RunID, p.ID); err != nil {
			return res.Applied, res.Skipped, err
		}
	}
	r.audit(ctx, "changeset.undo", changesetTarget(id), map[string]any{"restored": res.Applied, "kept_edited": res.Skipped, "settings": len(applied)})
	return res.Applied, res.Skipped, nil
}
