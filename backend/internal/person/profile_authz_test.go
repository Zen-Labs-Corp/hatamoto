package person

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/authz"
	"hatamoto.dev/core/internal/authzctx"
	"hatamoto.dev/core/internal/authzsubject"
	"hatamoto.dev/core/internal/entity"
)

// guardedWorld asks the entity authorizer before every write, like the entity service does.
type guardedWorld struct{ *fakeWorld }

func (g guardedWorld) CreateEntity(ctx context.Context, by *uuid.UUID, in *entity.CreateEntityInput) (*entity.Entity, error) {
	if err := entity.Authorize(ctx, authz.ActionEntityCreate, authz.Object{Kind: "entity", Schema: "person", Status: "draft"}); err != nil {
		return nil, err
	}
	return g.fakeWorld.CreateEntity(ctx, by, in)
}

func (g guardedWorld) UpdateEntity(ctx context.Context, id uuid.UUID, by *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error) {
	// The person was made by somebody else (NULL creator), so own-object rules cannot match.
	if err := entity.Authorize(ctx, authz.ActionEntityUpdate, authz.Object{Kind: "entity", ID: id.String(), Schema: "person", Status: "draft"}); err != nil {
		return nil, err
	}
	return g.fakeWorld.UpdateEntity(ctx, id, by, in)
}

// A user saving their own profile works for every role: the route authorizes profile.update, the person is theirs.
func TestSaveOwnProfileIgnoresContentRules(t *testing.T) {
	for _, role := range []string{"read-only", "reviewer", "contributor", "author"} {
		t.Run(role, func(t *testing.T) {
			user := uuid.New()
			ctx := authzctx.With(context.Background(), authzsubject.ForSession(user.String(), role))
			w := newWorld()
			p := &Profiles{profiles: w, entities: guardedWorld{w}}
			if _, err := p.Save(ctx, user, ProfileInput{Name: "Alex Carter", Photo: "/a.jpg"}); err != nil {
				t.Fatalf("first save: %v", err)
			}
			if _, err := p.Save(ctx, user, ProfileInput{Name: "Alex C", Photo: "/a.jpg"}); err != nil {
				t.Fatalf("second save: %v", err)
			}
		})
	}
}
