package admin

import (
	"context"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/person"
)

// PeopleStore is *person.Profiles as the profile pages and the People screens
// use it. Nil only in route-table builds and in tests that do not touch
// people; the profile gate then lets every request through.
type PeopleStore interface {
	// ForUser returns the person a user owns, person.ErrNotFound when none.
	ForUser(ctx context.Context, userID uuid.UUID) (*person.Person, error)
	// Save writes the profile form, making the person from the typed name when
	// the user has none; invalid input is a *person.ValidationError.
	Save(ctx context.Context, userID uuid.UUID, in person.ProfileInput) (*person.Person, error)

	// List returns every person by name.
	List(ctx context.Context) ([]*person.Person, error)
	// PostCounts is the number of published entities credited to each person.
	PostCounts(ctx context.Context) (map[uuid.UUID]int, error)
	// LinkedUsers maps a person to the user that owns it (people without a
	// login are absent).
	LinkedUsers(ctx context.Context) (map[uuid.UUID]uuid.UUID, error)
	// Create makes a person without a login; a bad name is a *person.ValidationError.
	Create(ctx context.Context, by *uuid.UUID, name string) (*person.Person, error)
	// MergePeople folds drop into keep (person.ErrMergeBothLogins when both
	// have a login).
	MergePeople(ctx context.Context, keepID, dropID uuid.UUID) (*person.MergeResult, error)
}

var _ PeopleStore = (*person.Profiles)(nil)
