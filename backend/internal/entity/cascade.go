package entity

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Querier is the read side of a pool or a transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// DeleteCascade reads what an entity delete removes with it (inside the
// transaction when q is one) and returns what to announce once the delete
// committed. The entity package never imports the packages behind it.
type DeleteCascade interface {
	CollectForEntity(ctx context.Context, q Querier, id uuid.UUID) (announce func(context.Context), err error)
}

// cascadeHook holds the cascade and what a changeset delete collected until its
// transaction is known to have committed.
type cascadeHook struct {
	c       DeleteCascade
	q       Querier // reads outside a transaction (DeleteEntity)
	mu      sync.Mutex
	pending map[uuid.UUID]func(context.Context)
}

// SetDeleteCascade registers the cascade; q serves reads made outside a
// transaction. Call before the service is used concurrently.
func (s *Service) SetDeleteCascade(c DeleteCascade, q Querier) {
	s.cascade = &cascadeHook{c: c, q: q, pending: map[uuid.UUID]func(context.Context){}}
}

// collectCascade reads the cascade of a delete. An error aborts the delete.
func (s *Service) collectCascade(ctx context.Context, q Querier, id uuid.UUID) (func(context.Context), error) {
	if s.cascade == nil {
		return nil, nil
	}
	if q == nil {
		q = s.cascade.q
	}
	announce, err := s.cascade.c.CollectForEntity(ctx, q, id)
	if err != nil {
		return nil, fmt.Errorf("failed to read what the delete cascades: %w", err)
	}
	return announce, nil
}

// holdCascade keeps a changeset delete's announcement until AfterChangeset
// (commit) or DropCascade (rollback).
func (s *Service) holdCascade(id uuid.UUID, announce func(context.Context)) {
	if s.cascade == nil || announce == nil {
		return
	}
	s.cascade.mu.Lock()
	s.cascade.pending[id] = announce
	s.cascade.mu.Unlock()
}

// takeCascade removes and returns the held announcement of id.
func (s *Service) takeCascade(id uuid.UUID) func(context.Context) {
	if s.cascade == nil {
		return nil
	}
	s.cascade.mu.Lock()
	defer s.cascade.mu.Unlock()
	a := s.cascade.pending[id]
	delete(s.cascade.pending, id)
	return a
}

// DropCascade forgets the held announcement of a changeset delete that was
// rolled back.
func (s *Service) DropCascade(id uuid.UUID) { s.takeCascade(id) }
