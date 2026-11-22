package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/changeset"
	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/reviewstate"
)

func TestStageOf(t *testing.T) {
	two := []changeset.StageSpec{{Name: "Legal", Group: "legal"}, {Name: "Brand", Group: "brand"}}
	for _, c := range []struct {
		name string
		row  changeset.EditRow
		want admin.ReviewStage
	}{
		{"first stage", changeset.EditRow{Stages: two}, admin.ReviewStage{Index: 0, Total: 2, Name: "Legal", Group: "legal"}},
		{"second stage", changeset.EditRow{Stages: two, Approvals: 1}, admin.ReviewStage{Index: 1, Total: 2, Name: "Brand", Group: "brand"}},
		{"all approved", changeset.EditRow{Stages: two, Approvals: 2}, admin.ReviewStage{}},
		{"rejected", changeset.EditRow{Stages: two, Rejected: true}, admin.ReviewStage{}},
		{"no stages", changeset.EditRow{}, admin.ReviewStage{}},
	} {
		if got := stageOf(c.row); got != c.want {
			t.Errorf("%s: stageOf = %+v, want %+v", c.name, got, c.want)
		}
	}
}

type yesGroups struct{}

func (yesGroups) MayDecide(context.Context, string, string, string, string) (bool, error) {
	return true, nil
}

// NeedsGroup is the free-core notice: set for a group stage only while no resolver defines groups.
func TestApprovalStateNeedsGroupDependsOnResolver(t *testing.T) {
	reviewstate.Reset()
	t.Cleanup(reviewstate.Reset)
	legal := changeset.EditRow{Stages: []changeset.StageSpec{{Name: "Legal", Group: "legal"}}}
	def := changeset.EditRow{Stages: []changeset.StageSpec{{Name: "Review", Group: reviewstate.DefaultGroup}}}
	done := changeset.EditRow{Stages: legal.Stages, Approvals: 1}

	if _, need := approvalState(legal); need != "legal" {
		t.Errorf("no resolver: NeedsGroup = %q, want legal", need)
	}
	if _, need := approvalState(def); need != "" {
		t.Errorf("default group: NeedsGroup = %q, want empty", need)
	}
	if ok, need := approvalState(done); !ok || need != "" {
		t.Errorf("all approved = %v, %q", ok, need)
	}
	legal.Rejected = true
	if ok, need := approvalState(legal); ok || need != "" {
		t.Errorf("rejected = %v, %q", ok, need)
	}

	reviewstate.RegisterGroups(yesGroups{})
	legal.Rejected = false
	if _, need := approvalState(legal); need != "" {
		t.Errorf("with a resolver: NeedsGroup = %q, want empty", need)
	}
}

type schemaList struct {
	entity.SchemaRepository
	err error
}

func (l schemaList) List(context.Context) ([]*entity.EntitySchema, error) {
	return []*entity.EntitySchema{{Slug: "page", Name: "Page"}}, l.err
}

func sectionServer(t *testing.T, listErr error, locales *entity.LocaleSet) *Server {
	t.Helper()
	svc := entity.NewService(schemaList{err: listErr}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if locales != nil {
		svc.URLRegistry().SetLocales(func(context.Context) (*entity.LocaleSet, error) { return locales, nil })
	}
	return &Server{entityService: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func pageEntity(slug, locale string) *entity.Entity {
	return &entity.Entity{ID: uuid.New(), Slug: slug, Locale: locale, Schema: &entity.EntitySchema{Slug: "page"}}
}

func TestReviewSectionWiring(t *testing.T) {
	ctx := context.Background()
	fr, err := entity.NewLocaleSet("en", []entity.LocaleEntry{{Tag: "en", Segment: "en"}, {Tag: "fr", Segment: "fr"}}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	s := sectionServer(t, nil, fr)
	for _, c := range []struct {
		name string
		e    *entity.Entity
		want string
	}{
		{"default locale", pageEntity("pricing", "en"), "/pricing"},
		{"prefixed locale", pageEntity("pricing", "fr"), "/pricing"},
		{"no schema", &entity.Entity{Slug: "x"}, ""},
		{"unknown schema", &entity.Entity{Slug: "x", Schema: &entity.EntitySchema{Slug: "nope"}}, ""},
	} {
		got, err := s.reviewSection(ctx, nil, c.e)
		if err != nil || got != c.want {
			t.Errorf("%s: reviewSection = %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

// A registry that cannot be read is an error, not an empty section.
func TestReviewSectionRegistryFailure(t *testing.T) {
	s := sectionServer(t, errors.New("db down"), nil)
	if got, err := s.reviewSection(context.Background(), nil, pageEntity("pricing", "en")); err == nil {
		t.Fatalf("reviewSection = %q, nil; want an error", got)
	}
}
