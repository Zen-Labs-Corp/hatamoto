package admin

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/user"
)

// Shared helpers of the content-rule tests. Tests of one area prefix any extra top-level identifier with
// their file stem, so parallel areas never collide.

// newRolesRig is newContentRig with one author, contributor, reviewer and read-only user added, and the
// entity store wrapped so every write asks the authorizer about the stored row, as the entity service does.
func newRolesRig(t *testing.T, adjust ...func(*Deps)) (*contentRig, map[user.Role]*user.User) {
	t.Helper()
	users := map[user.Role]*user.User{}
	c := newContentRigWith(t, func(d *Deps) {
		fu := d.Users.(fakeUsers)
		for _, r := range []user.Role{user.RoleAuthor, user.RoleContributor, user.RoleReviewer, user.RoleReadOnly} {
			name := string(r) + " Sam"
			u := &user.User{ID: uuid.New(), Email: string(r) + "@example.com", Role: r, Name: &name}
			fu[u.ID] = u
			users[r] = u
		}
		d.Content.Entities = authorizingEntities{EntityStore: d.Content.Entities}
		for _, f := range adjust {
			f(d)
		}
	})
	return c, users
}

// authorizingEntities asks entity.Authorize about the stored row before each write, like entity.Service does
// for a caller with a subject on the context, then delegates to the fake.
type authorizingEntities struct{ EntityStore }

func (s authorizingEntities) check(ctx context.Context, id uuid.UUID, act func(*entity.Entity) authz.Action) error {
	e, err := s.EntityStore.GetEntity(ctx, id)
	if err != nil {
		return err
	}
	return entity.Authorize(ctx, act(e), e.AuthzObject())
}

func fixedAction(a authz.Action) func(*entity.Entity) authz.Action {
	return func(*entity.Entity) authz.Action { return a }
}

func (s authorizingEntities) CreateEntity(ctx context.Context, by *uuid.UUID, in *entity.CreateEntityInput) (*entity.Entity, error) {
	sc, err := s.EntityStore.GetSchema(ctx, in.SchemaID)
	if err != nil {
		return nil, err
	}
	if err := entity.Authorize(ctx, authz.ActionEntityCreate, authz.Object{Kind: "entity", Schema: sc.Slug, Locale: in.Locale}); err != nil {
		return nil, err
	}
	return s.EntityStore.CreateEntity(ctx, by, in)
}

func (s authorizingEntities) UpdateEntity(ctx context.Context, id uuid.UUID, by *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	if err := s.check(ctx, id, fixedAction(authz.ActionEntityUpdate)); err != nil {
		return nil, err
	}
	return s.EntityStore.UpdateEntity(ctx, id, by, in)
}

func (s authorizingEntities) UpdateEntityStatus(ctx context.Context, id uuid.UUID, by *uuid.UUID, st entity.EntityStatus) (*entity.Entity, error) {
	live := func(x entity.EntityStatus) bool { return x == entity.StatusPublished || x == entity.StatusScheduled }
	err := s.check(ctx, id, func(e *entity.Entity) authz.Action {
		switch {
		case live(st):
			return authz.ActionEntityPublish
		case live(e.Status):
			return authz.ActionEntityUnpublish
		}
		return authz.ActionEntityUpdate
	})
	if err != nil {
		return nil, err
	}
	return s.EntityStore.UpdateEntityStatus(ctx, id, by, st)
}

func (s authorizingEntities) ScheduleEntity(ctx context.Context, id uuid.UUID, by *uuid.UUID, at time.Time) (*entity.Entity, error) {
	if err := s.check(ctx, id, fixedAction(authz.ActionEntityPublish)); err != nil {
		return nil, err
	}
	return s.EntityStore.ScheduleEntity(ctx, id, by, at)
}

func (s authorizingEntities) DeleteEntity(ctx context.Context, id uuid.UUID) error {
	if err := s.check(ctx, id, fixedAction(authz.ActionEntityDelete)); err != nil {
		return err
	}
	return s.EntityStore.DeleteEntity(ctx, id)
}

func (s authorizingEntities) RestoreVersion(ctx context.Context, id uuid.UUID, by *uuid.UUID, n int) (*entity.Entity, error) {
	if err := s.check(ctx, id, fixedAction(authz.ActionEntityVersionRestore)); err != nil {
		return nil, err
	}
	return s.EntityStore.RestoreVersion(ctx, id, by, n)
}

// entityBy stores a service entity created by owner (nil: no known creator) in the given status and returns it.
func (c *contentRig) entityBy(owner *user.User, status entity.EntityStatus) *entity.Entity {
	c.ents.mu.Lock()
	defer c.ents.mu.Unlock()
	c.ents.nextID++
	e := c.ents.addEntity(c.ents.nextID, c.service, "By "+uuid.NewString()[:6], "by-"+uuid.NewString()[:8], status, `{"summary":"s"}`, t0)
	if owner != nil {
		e.CreatedBy = &owner.ID
	}
	return e
}
