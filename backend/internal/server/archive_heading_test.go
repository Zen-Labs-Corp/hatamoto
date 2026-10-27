package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/entity"
)

func TestArchiveHeadingPrecedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		schema   entity.EntitySchema
		wantName string
		wantDesc string
	}{
		{"settings title wins", entity.EntitySchema{Name: "Team Member", Slug: "team_member", Settings: json.RawMessage(`{"archive_title":"  Meet the team ","archive_description":" Our people "}`)}, "Meet the team", "Our people"},
		{"schema name next", entity.EntitySchema{Name: "Team Member", Slug: "team_member", Settings: json.RawMessage(`{"archive_path":"/team"}`)}, "Team Member", ""},
		{"whitespace title falls through", entity.EntitySchema{Name: "Team Member", Slug: "team_member", Settings: json.RawMessage(`{"archive_title":"   "}`)}, "Team Member", ""},
		{"humanised slug last", entity.EntitySchema{Name: "", Slug: "team_member"}, "Team member", ""},
		{"description without title", entity.EntitySchema{Name: "Post", Slug: "post", Settings: json.RawMessage(`{"archive_description":"News"}`)}, "Post", "News"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := tt.schema
			s.ID = uuid.New()
			snap, err := entity.BuildURLSnapshot([]*entity.EntitySchema{&s}, nil)
			if err != nil {
				t.Fatal(err)
			}
			cfg, ok := snap.Config(s.Slug)
			if !ok {
				t.Fatal("no config")
			}
			title, desc := archiveHeading(cfg, s.Slug)
			if title != tt.wantName || desc != tt.wantDesc {
				t.Errorf("got (%q, %q), want (%q, %q)", title, desc, tt.wantName, tt.wantDesc)
			}
		})
	}
	if title, desc := archiveHeading(nil, "team_member"); title != "Team member" || desc != "" {
		t.Errorf("nil cfg: (%q, %q)", title, desc)
	}
}

func TestResolveEntity_ArchiveTitle(t *testing.T) {
	t.Parallel()
	snap := fixtureSnapshot(t, map[string]string{
		"person": `{"url_pattern":"/team/{slug}","archive_path":"/team","archive_title":"Our team","archive_description":"Who we are"}`,
	})
	got, err := resolveEntity(context.Background(), &fakeResolveRepo{}, snap, "/team")
	if err != nil || got == nil || got.Archive == nil {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Archive.Title != "Our team" || got.Archive.Description != "Who we are" {
		t.Errorf("archive: %+v", got.Archive)
	}
}
