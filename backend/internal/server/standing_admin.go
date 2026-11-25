package server

import (
	"context"
	"errors"
	"slices"
	"time"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/standing"
)

// ErrStandingUnavailable means the services the approvals need are not set up.
var ErrStandingUnavailable = errors.New("automatic updates are not available")

// standingAdmin serves the Automatic updates tab (admin.StandingApprovals).
// It builds the service on each call: the admin is mounted before the
// changeset engine and the plugin runtime exist.
type standingAdmin struct {
	svc    func() *standingService
	digest func(ctx context.Context, since time.Time) ([]standing.DigestRow, error)
}

var _ admin.StandingApprovals = standingAdmin{}

func (s *Server) standingAdmin() standingAdmin {
	return standingAdmin{svc: s.standingServiceFor, digest: func(ctx context.Context, since time.Time) ([]standing.DigestRow, error) {
		if s.db == nil {
			return nil, ErrStandingUnavailable
		}
		return (&standing.Store{DB: s.db}).Digest(ctx, since)
	}}
}

func (a standingAdmin) Rule(ctx context.Context, plugin string) (admin.StandingRule, bool) {
	svc := a.svc()
	if svc == nil {
		return admin.StandingRule{}, false
	}
	r, ok := svc.Policy(plugin)
	if !ok {
		return admin.StandingRule{}, false
	}
	rule := admin.StandingRule{Version: r.Version, Hash: r.Hash, Run: r.Run, Fields: r.Policy.Fields, Schemas: r.Policy.Schemas, MaxPerHour: r.Policy.MaxPerHour}
	if c := r.Policy.Create; c != nil {
		rule.CreateSchemas, rule.CreateMaxPerHour = c.Schemas, c.MaxPerHour
		rule.LinkFieldLabels = svc.linkFieldLabels(ctx, c)
	}
	return rule, true
}

func (a standingAdmin) Active(ctx context.Context, plugin, run string) (*admin.StandingApproval, error) {
	svc := a.svc()
	if svc == nil {
		return nil, ErrStandingUnavailable
	}
	ap, err := svc.Active(ctx, plugin, run)
	if err != nil || ap == nil {
		return nil, err
	}
	return &admin.StandingApproval{By: ap.ApprovedBy, At: ap.ApprovedAt}, nil
}

func (a standingAdmin) Paused(ctx context.Context, plugin, run string) (*admin.StandingPaused, error) {
	svc := a.svc()
	if svc == nil {
		return nil, ErrStandingUnavailable
	}
	v, err := svc.Paused(ctx, plugin, run)
	if err != nil || v == nil {
		return nil, err
	}
	return &admin.StandingPaused{At: v.At, Reason: v.Reason}, nil
}

func (a standingAdmin) Approve(ctx context.Context, plugin, run, by, version, hash string) error {
	svc := a.svc()
	if svc == nil {
		return ErrStandingUnavailable
	}
	return svc.Approve(ctx, plugin, run, by, version, hash)
}

func (a standingAdmin) TurnOff(ctx context.Context, plugin string) error {
	svc := a.svc()
	if svc == nil {
		return ErrStandingUnavailable
	}
	return svc.TurnOff(ctx, plugin)
}

func (a standingAdmin) Waiting(ctx context.Context, plugin string) ([]admin.StandingWaiting, error) {
	svc := a.svc()
	if svc == nil {
		return nil, ErrStandingUnavailable
	}
	rows, err := svc.Waiting(ctx, plugin)
	if err != nil {
		return nil, err
	}
	out := make([]admin.StandingWaiting, len(rows))
	for i, w := range rows {
		out[i] = admin.StandingWaiting{RunID: w.RunID, Run: w.Run, ChangesetID: w.ChangesetID, Items: w.Items, At: w.At}
	}
	return out, nil
}

func (a standingAdmin) Digest(ctx context.Context, since time.Time) ([]admin.StandingDigestRow, error) {
	rows, err := a.digest(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]admin.StandingDigestRow, len(rows))
	for i, d := range rows {
		out[i] = admin.StandingDigestRow{Plugin: d.Plugin, Changes: d.Changes, Created: d.Created, Hosts: d.Hosts}
	}
	return out, nil
}

// linkFieldLabels names each link field by the title of the first create schema
// that has it, else by the humanised key.
func (s *standingService) linkFieldLabels(ctx context.Context, c *standing.CreatePolicy) []string {
	var tops [][]schemaform.TopField
	for _, slug := range c.Schemas {
		if s.schemas == nil {
			break
		}
		if raw, ok, err := s.schemas(ctx, slug); err == nil && ok {
			tops = append(tops, schemaform.TopFields(raw))
		}
	}
	labels := make([]string, 0, len(c.LinkFields))
	for _, key := range c.LinkFields {
		label := schemaform.Humanize(key)
		for _, fields := range tops {
			if i := slices.IndexFunc(fields, func(f schemaform.TopField) bool { return f.Key == key }); i >= 0 {
				label = fields[i].Label
				break
			}
		}
		labels = append(labels, label)
	}
	return labels
}
