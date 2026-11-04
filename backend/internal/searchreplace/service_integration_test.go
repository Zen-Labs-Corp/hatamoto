//go:build integration

package searchreplace

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/config"
	"hatamoto.dev/core/internal/database"
	"hatamoto.dev/core/internal/entity"
)

func integrationService(t *testing.T) (*entity.Service, uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	db, err := database.New(ctx, config.DatabaseConfig{URL: dsn, MaxConnections: 4, MinConnections: 1, MaxConnLifetime: 30 * time.Minute, MaxConnIdleTime: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	schemaID := uuid.New()
	slug := "sr-" + uuid.NewString()[:8]
	if _, err := db.Pool().Exec(ctx, `INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{}')`, schemaID, slug); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = db.Pool().Exec(c, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return entity.NewService(entity.NewPostgresSchemaRepository(db), entity.NewPostgresEntityRepository(db), logger), schemaID
}

func TestSearchReplaceIntegration(t *testing.T) {
	ctx := context.Background()
	svc, schemaID := integrationService(t)
	tag := "zq" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	mk := func(slug, attrs string) *entity.Entity {
		e, err := svc.CreateEntity(ctx, nil, &entity.CreateEntityInput{SchemaID: schemaID, Name: slug, Slug: slug, Locale: "en", Attributes: json.RawMessage(attrs)})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	a := mk("a-"+tag, `{"body":"<p>visit `+tag+` today</p>","meta_title":"`+tag+` deals"}`)
	b := mk("b-"+tag, `{"body":"<p>`+tag+`</p>"}`)
	s := &Service{Ents: svc}
	o := Options{Find: tag, Replace: "fresh"}
	scopes := ScopeContent | ScopeSEO | ScopeOther

	preview := func() *Preview {
		t.Helper()
		p, err := s.Preview(ctx, o, scopes)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := preview()
	if p.Total != 3 || p.TotalItems != 2 {
		t.Fatalf("preview %+v", p)
	}

	// A2: apply the body match of A only; one new version; restore brings the text back.
	var pickA, pickB []string
	for _, g := range p.Groups {
		for _, m := range g.Matches {
			switch {
			case g.ID == a.ID && strings.HasPrefix(m.Label, "Body"):
				pickA = append(pickA, m.Token)
			case g.ID == b.ID:
				pickB = append(pickB, m.Token)
			}
		}
	}
	if len(pickA) != 1 || len(pickB) != 1 {
		t.Fatalf("tokens %v %v", pickA, pickB)
	}

	// A3: B is edited between preview and apply.
	if _, err := svc.UpdateEntity(ctx, b.ID, nil, &entity.UpdateEntityInput{Attributes: rawp(`{"body":"<p>` + tag + ` edited</p>"}`)}); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Apply(ctx, nil, o, scopes, append(pickA, pickB...))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Replaced != 1 || rep.Items != 1 || rep.Skipped != 1 || len(rep.Failed) != 0 {
		t.Fatalf("report %+v", rep)
	}
	gotA, _ := svc.GetEntity(ctx, a.ID)
	if gotA.Version != a.Version+1 || !strings.Contains(string(gotA.Attributes), "visit fresh today") || !strings.Contains(string(gotA.Attributes), tag+" deals") {
		t.Fatalf("a after apply: v%d %s", gotA.Version, gotA.Attributes)
	}
	gotB, _ := svc.GetEntity(ctx, b.ID)
	if !strings.Contains(string(gotB.Attributes), tag+" edited") {
		t.Errorf("stale entity overwritten: %s", gotB.Attributes)
	}
	versions, err := svc.ListVersions(ctx, a.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var searchVersion *entity.EntityVersion
	for _, v := range versions {
		if v.ChangeSummary != nil && strings.Contains(*v.ChangeSummary, "Search and replace") {
			searchVersion = v
		}
	}
	if searchVersion == nil {
		t.Fatalf("no version names the search and replace: %+v", versions)
	}

	restored, err := svc.RestoreVersion(ctx, a.ID, nil, searchVersion.Version)
	if err != nil || !strings.Contains(string(restored.Attributes), "visit "+tag+" today") {
		t.Fatalf("restore: %v %s", err, restored.Attributes)
	}
}

func rawp(s string) *json.RawMessage { r := json.RawMessage(s); return &r }
