package person

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/author"
	"hatamoto.dev/core/internal/entity"
)

// Field limits of the profile form.
const (
	maxProfileName     = 120
	maxProfileJobTitle = 120
	maxProfileShortBio = 300
)

// Complete reports whether a profile has what a signed-in user must provide:
// a name and a photo. The admin sends users with an incomplete profile to the
// "Complete your profile" step.
func Complete(p *Person) bool {
	return p != nil && strings.TrimSpace(p.Name) != "" && strings.TrimSpace(p.Photo) != ""
}

// ProfileInput is what the profile forms edit. The person's other attributes
// (bio, public email, links other than LinkedIn, expertise) are left alone.
type ProfileInput struct {
	Name     string
	Photo    string
	JobTitle string
	ShortBio string
	LinkedIn string
	Public   bool
}

// ValidationError lists the problems of a ProfileInput by form field name
// ("name", "photo", "job_title", "short_bio", "linkedin").
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string {
	return fmt.Sprintf("invalid profile: %d field(s)", len(e.Fields))
}

// Validate trims the input and checks it. Name and photo are required; the
// rest is optional. It returns nil when the input is good.
func (in *ProfileInput) Validate() *ValidationError {
	in.Name, in.Photo = strings.TrimSpace(in.Name), strings.TrimSpace(in.Photo)
	in.JobTitle, in.ShortBio, in.LinkedIn = strings.TrimSpace(in.JobTitle), strings.TrimSpace(in.ShortBio), strings.TrimSpace(in.LinkedIn)
	errs := map[string]string{}
	switch {
	case in.Name == "":
		errs["name"] = "Enter your name"
	case utf8.RuneCountInString(in.Name) > maxProfileName:
		errs["name"] = fmt.Sprintf("Use at most %d characters", maxProfileName)
	}
	if in.Photo == "" {
		errs["photo"] = "Add a photo"
	}
	if utf8.RuneCountInString(in.JobTitle) > maxProfileJobTitle {
		errs["job_title"] = fmt.Sprintf("Use at most %d characters", maxProfileJobTitle)
	}
	if utf8.RuneCountInString(in.ShortBio) > maxProfileShortBio {
		errs["short_bio"] = fmt.Sprintf("Use at most %d characters", maxProfileShortBio)
	}
	if in.LinkedIn != "" {
		if u, err := url.Parse(in.LinkedIn); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs["linkedin"] = "Enter a full web address, starting with https://"
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &ValidationError{Fields: errs}
}

// EntityWriter is the part of entity.Service that writes people. Going through
// it keeps revisions, validation and the author mirror (PersonChanged) working.
type EntityWriter interface {
	GetEntity(ctx context.Context, id uuid.UUID) (*entity.Entity, error)
	CreateEntity(ctx context.Context, userID *uuid.UUID, in *entity.CreateEntityInput) (*entity.Entity, error)
	UpdateEntity(ctx context.Context, id uuid.UUID, userID *uuid.UUID, in *entity.UpdateEntityInput) (*entity.Entity, error)
	DeleteEntity(ctx context.Context, id uuid.UUID) error
}

// ProfileStore is the part of Store the profile service reads and links with.
type ProfileStore interface {
	SchemaID(ctx context.Context) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (*Person, error)
	ForUser(ctx context.Context, userID uuid.UUID) (*Person, error)
	Link(ctx context.Context, userID, personID uuid.UUID) error
}

// Profiles creates and edits the person a user account owns. The embedded
// Store gives it the read methods too.
type Profiles struct {
	*Store
	profiles ProfileStore
	entities EntityWriter
	// pathOf and mover leave a redirect when people are merged (WithRedirects).
	pathOf func(ctx context.Context, slug string) string
	mover  PathMover
}

// NewProfiles returns the profile service over a store and the entity service.
func NewProfiles(store *Store, entities EntityWriter) *Profiles {
	return &Profiles{Store: store, profiles: store, entities: entities}
}

// maxSlugTries bounds the -2, -3, ... search for a free slug.
const maxSlugTries = 50

// ensureForUser returns the person of a user, making one named name (linked
// to the user, a draft until Public is on) when there is none yet. The name is
// the one the user typed: nothing is ever derived from the email.
func (p *Profiles) ensureForUser(ctx context.Context, userID uuid.UUID, name string) (*Person, error) {
	got, err := p.profiles.ForUser(ctx, userID)
	if err == nil {
		return got, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	created, err := p.create(ctx, &userID, name)
	if err != nil {
		return nil, err
	}
	if err := p.profiles.Link(ctx, userID, created.ID); err != nil {
		// A parallel request linked first: keep its person, drop ours.
		if errors.Is(err, ErrAlreadyLinked) {
			_ = p.entities.DeleteEntity(ctx, created.ID)
			return p.profiles.ForUser(ctx, userID)
		}
		return nil, err
	}
	return p.profiles.Get(ctx, created.ID)
}

// Create makes a draft person that is not public and has no login (People,
// Add person). The name must be 1 to 120 characters; a bad one is a
// *ValidationError on the "name" field.
func (p *Profiles) Create(ctx context.Context, by *uuid.UUID, name string) (*Person, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return nil, &ValidationError{Fields: map[string]string{"name": "Enter a name"}}
	case utf8.RuneCountInString(name) > maxProfileName:
		return nil, &ValidationError{Fields: map[string]string{"name": fmt.Sprintf("Use at most %d characters", maxProfileName)}}
	}
	created, err := p.create(ctx, by, name)
	if err != nil {
		return nil, err
	}
	return p.profiles.Get(ctx, created.ID)
}

// create inserts the person entity (a draft: status follows Public), with the first free slug.
func (p *Profiles) create(ctx context.Context, by *uuid.UUID, name string) (*entity.Entity, error) {
	schemaID, err := p.profiles.SchemaID(ctx)
	if err != nil {
		return nil, err
	}
	slug := author.Slugify(name)
	var created *entity.Entity
	for n := 1; n <= maxSlugTries; n++ {
		try := slug
		if n > 1 {
			try = fmt.Sprintf("%s-%d", slug, n)
		}
		created, err = p.entities.CreateEntity(ctx, by, &entity.CreateEntityInput{
			SchemaID: schemaID, Name: name, Slug: try, Attributes: json.RawMessage(`{"public":false}`),
		})
		if !errors.Is(err, entity.ErrSlugAlreadyExists) {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("create person: %w", err)
	}
	return created, nil
}

// Save writes a profile into the user's person, making the person first (from
// the typed name) when the user has none. Invalid input returns a
// *ValidationError and writes nothing. Empty optional values remove their
// attribute; every other attribute is kept.
func (p *Profiles) Save(ctx context.Context, userID uuid.UUID, in ProfileInput) (*Person, error) {
	if verr := in.Validate(); verr != nil {
		return nil, verr
	}
	cur, err := p.ensureForUser(ctx, userID, in.Name)
	if err != nil {
		return nil, err
	}
	return p.update(ctx, cur.ID, &userID, in)
}

// update writes a validated profile into one person entity.
func (p *Profiles) update(ctx context.Context, personID uuid.UUID, by *uuid.UUID, in ProfileInput) (*Person, error) {
	e, err := p.entities.GetEntity(ctx, personID)
	if err != nil {
		return nil, err
	}
	attrs := map[string]any{}
	if len(e.Attributes) > 0 {
		if err := json.Unmarshal(e.Attributes, &attrs); err != nil {
			return nil, fmt.Errorf("read person attributes: %w", err)
		}
	}
	set := func(key, val string) {
		if val == "" {
			delete(attrs, key)
		} else {
			attrs[key] = val
		}
	}
	set("photo", in.Photo)
	set("job_title", in.JobTitle)
	set("short_bio", in.ShortBio)
	set("linkedin", in.LinkedIn)
	attrs["public"] = in.Public
	raw, err := json.Marshal(attrs)
	if err != nil {
		return nil, err
	}
	msg := json.RawMessage(raw)
	up := &entity.UpdateEntityInput{Name: &in.Name, Attributes: &msg}
	if e.PublishedAt != nil {
		// Has been public: the address stays, links to it may exist.
		if _, err := p.entities.UpdateEntity(ctx, personID, by, up); err != nil {
			return nil, err
		}
		return p.profiles.Get(ctx, personID)
	}
	// Never public: the address follows the saved name, -2, -3 on collision.
	base := author.Slugify(in.Name)
	for n := 1; n <= maxSlugTries; n++ {
		try := base
		if n > 1 {
			try = fmt.Sprintf("%s-%d", base, n)
		}
		up.Slug = &try
		_, err = p.entities.UpdateEntity(ctx, personID, by, up)
		if !errors.Is(err, entity.ErrSlugAlreadyExists) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	return p.profiles.Get(ctx, personID)
}
