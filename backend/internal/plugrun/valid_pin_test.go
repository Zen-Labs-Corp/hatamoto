package plugrun

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// The store checks every JSON document a plugin sends before it reaches the
// database, whose jsonb accepts a repeated member name. These tests run the
// checks with no database: a rejected document returns ErrInvalid, an accepted
// one goes on to the query, which panics on the missing connection.
//
// The invalid UTF-8 case is deliberately not asserted yet: the checks accepted
// it before the cutover and the cutover decides it (Postgres rejects it).

func accepted(t *testing.T, call func(s *Store) error) (ok bool) {
	t.Helper()
	defer func() {
		if recover() != nil {
			ok = true // got past validation to the database call
		}
	}()
	err := call(&Store{})
	return !errors.Is(err, ErrInvalid)
}

func TestValidAcceptsRepeatedNames(t *testing.T) {
	ctx, id := context.Background(), uuid.New()
	calls := map[string]func(doc string) func(*Store) error{
		"Progress": func(d string) func(*Store) error {
			return func(s *Store) error { return s.Progress(ctx, id, []byte(d)) }
		},
		"Finish": func(d string) func(*Store) error { return func(s *Store) error { return s.Finish(ctx, id, []byte(d)) } },
		"AddProposal": func(d string) func(*Store) error {
			return func(s *Store) error { return s.AddProposal(ctx, id, "k", []byte(d)) }
		},
		"SetProposalBefore": func(d string) func(*Store) error {
			return func(s *Store) error { return s.SetProposalBefore(ctx, id, 1, []byte(d)) }
		},
		"SetProposalDecided": func(d string) func(*Store) error {
			return func(s *Store) error { return s.SetProposalDecision(ctx, id, 1, ProposalApproved, []byte(d)) }
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			for _, doc := range []string{`{"a":1,"a":2}`, `{"a":1,"A":2}`, `[1,2]`, `"x"`, `7`, ` {"a":{"b":1,"b":2}} `} {
				if !accepted(t, call(doc)) {
					t.Errorf("%s was refused", doc)
				}
			}
			for _, doc := range []string{`{"a":`, `{a:1}`, `{"a":1} {"b":2}`, `[1,]`} {
				if accepted(t, call(doc)) {
					t.Errorf("%s was accepted", doc)
				}
			}
		})
	}
}
