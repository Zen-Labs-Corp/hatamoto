//go:build integration

package plugrun

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProposalDecisions(t *testing.T) {
	s, ctx := newStore(t)
	r, err := s.Create(ctx, Run{Plugin: plugin()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_runs WHERE id = $1`, r.ID) })
	for _, k := range []string{"site_settings", "menu"} {
		if err := s.AddProposal(ctx, r.ID, k, json.RawMessage(`{"a":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := s.Proposals(ctx, r.ID)
	if err != nil || len(ps) != 2 || ps[0].Decision != "" || ps[0].Before != nil || ps[0].DecidedAt != nil {
		t.Fatalf("proposals: %+v %v", ps, err)
	}
	if n, _ := s.UndecidedProposals(ctx, r.ID); n != 2 {
		t.Fatalf("undecided = %d", n)
	}
	if err := s.SetProposalDecision(ctx, r.ID, ps[0].ID, ProposalApproved, json.RawMessage(`{"title":"old"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProposalDecision(ctx, r.ID, ps[0].ID, ProposalRejected, nil); !errors.Is(err, ErrProposalDecided) {
		t.Fatalf("second decide: %v", err)
	}
	if err := s.SetProposalDecision(ctx, r.ID, 0, ProposalApproved, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := s.SetProposalDecision(ctx, r.ID, ps[1].ID, "maybe", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad decision: %v", err)
	}
	if err := s.SetProposalDecision(ctx, r.ID, ps[1].ID, ProposalRejected, json.RawMessage(`{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	ps, _ = s.Proposals(ctx, r.ID)
	var before map[string]string
	if ps[0].Decision != ProposalApproved || ps[0].DecidedAt == nil || json.Unmarshal(ps[0].Before, &before) != nil || before["title"] != "old" {
		t.Fatalf("approved: %+v", ps[0])
	}
	if ps[1].Decision != ProposalRejected || ps[1].Before != nil {
		t.Fatalf("rejected: %+v", ps[1])
	}
	if n, _ := s.UndecidedProposals(ctx, r.ID); n != 0 {
		t.Fatalf("undecided = %d", n)
	}
	if err := s.ClearProposalDecision(ctx, r.ID, ps[1].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clear rejected: %v", err)
	}
	if err := s.ClearProposalDecision(ctx, r.ID, ps[0].ID); err != nil {
		t.Fatal(err)
	}
	ps, _ = s.Proposals(ctx, r.ID)
	if ps[0].Decision != ProposalReverted || ps[0].Before == nil {
		t.Fatalf("reverted: %+v", ps[0])
	}
}

func TestRecentRuns(t *testing.T) {
	s, ctx := newStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	tick := 0
	s.Now = func() time.Time { tick++; return base.Add(time.Duration(tick) * time.Second) }
	a, b := plugin(), plugin()
	var ids []uuid.UUID
	for _, p := range []string{a, b, a} {
		r, err := s.Create(ctx, Run{Plugin: p})
		if err != nil {
			// one active run per plugin: finish the earlier one first
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
		if err := s.Fail(ctx, r.ID, "done"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, id := range ids {
			s.DB.Pool().Exec(ctx, `DELETE FROM hatamoto_runs WHERE id = $1`, id)
		}
	})
	got, err := s.Recent(ctx, a, 10)
	if err != nil || len(got) != 2 || got[0].ID != ids[2] || got[1].ID != ids[0] {
		t.Fatalf("plugin filter: %v %v", got, err)
	}
	all, err := s.Recent(ctx, "", 200)
	if err != nil || len(all) < 3 {
		t.Fatalf("all: %d %v", len(all), err)
	}
	if one, _ := s.Recent(ctx, a, 1); len(one) != 1 || one[0].ID != ids[2] {
		t.Fatalf("limit: %v", one)
	}
}
