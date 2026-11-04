//go:build integration

package pluginhost

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/entity"
	"hatamoto.dev/core/internal/media"
	"hatamoto.dev/core/internal/plugrun"
	"hatamoto.dev/sdk"
)

// The fetch phase can reach the network, so its reads see only what an
// anonymous visitor sees: no drafts, hidden people, variants, locales the
// site does not serve, or media no public page uses. The transform phase
// keeps reading all published content.
func TestFetchPhaseReadsOnlyWhatVisitorsSee(t *testing.T) {
	var ids map[string]string
	var mediaIDs map[string]string
	a, rc, _ := runRig(t, func(_ *plugrun.Run, s *Services) {
		db := s.Runs.DB
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		schemas := entity.NewPostgresSchemaRepository(db)
		tag := uuid.NewString()[:8]
		sc, err := schemas.Create(context.Background(), &entity.CreateSchemaInput{Name: "fp " + tag, Slug: "fp-" + tag,
			Schema: json.RawMessage(`{"type":"object","properties":{}}`)})
		if err != nil {
			t.Fatal(err)
		}
		var personSchema uuid.UUID
		if err := db.Pool().QueryRow(context.Background(), `SELECT id FROM hatamoto_entity_schemas WHERE slug = 'person'`).Scan(&personSchema); err != nil {
			t.Fatal(err)
		}
		ids, mediaIDs = map[string]string{}, map[string]string{}
		ins := func(key string, schema uuid.UUID, status, locale, variant, attrs string) {
			var id string
			err := db.Pool().QueryRow(context.Background(), `
				INSERT INTO hatamoto_entities (schema_id, name, slug, status, locale, variant_key, attributes, published_at)
				VALUES ($1, $2, $2, $3, COALESCE(NULLIF($4, ''), hatamoto_site_language()), NULLIF($5, ''), $6::jsonb, now()) RETURNING id::text`,
				schema, key+"-"+tag, status, locale, variant, attrs).Scan(&id)
			if err != nil {
				t.Fatal(err)
			}
			ids[key] = id
		}
		ins("public", sc.ID, "published", "", "", `{"image":"/media/used-`+tag+`.png"}`)
		ins("draft", sc.ID, "draft", "", "", `{}`)
		ins("variant", sc.ID, "published", "", "v1", `{}`)
		ins("foreign-locale", sc.ID, "published", "zz", "", `{}`)
		ins("hidden-person", personSchema, "published", "", "", `{"public":false,"public_email":"a@example.com"}`)
		ins("public-person", personSchema, "published", "", "", `{"public":true}`)
		for key, file := range map[string]string{"used": "used-" + tag + ".png", "unused": "unused-" + tag + ".png"} {
			var id string
			if err := db.Pool().QueryRow(context.Background(), `
				INSERT INTO hatamoto_media (filename, original_filename, mime_type, file_size, storage_path)
				VALUES ($1, $1, 'image/png', 1, $1) RETURNING id::text`, file).Scan(&id); err != nil {
				t.Fatal(err)
			}
			mediaIDs[key] = id
		}
		t.Cleanup(func() {
			bg := context.Background()
			_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_entities WHERE name LIKE '%-' || $1`, tag)
			_, _ = db.Pool().Exec(bg, `DELETE FROM hatamoto_media WHERE filename LIKE '%-' || $1 || '.png'`, tag)
			_ = schemas.Delete(bg, sc.ID)
		})
		s.Entities = entity.NewService(schemas, entity.NewPostgresEntityRepository(db), log)
		s.Media = media.NewService(media.NewPostgresRepository(db), media.Config{UploadDir: t.TempDir()}, log)
		pub := entity.NewPostgresPublicRepository(db)
		s.PublicEntityIDs, s.PublicMediaStems = pub.PublicIDs, pub.PublicMediaStems
	})
	if err := a.grants.(*GrantStore).Set(context.Background(), rc.Plugin, []sdk.Permission{sdk.PermEntitiesRead, sdk.PermMediaRead}); err != nil {
		t.Fatal(err)
	}
	get := func(phase, fn string, args any) *sdk.HostError {
		c := rc
		c.Phase = phase
		_, he := callAs(t, a, c, fn, args)
		return he
	}
	for key, visible := range map[string]bool{"public": true, "public-person": true, "draft": false, "variant": false, "foreign-locale": false, "hidden-person": false} {
		he := get(sdk.RunPhaseFetch, sdk.FnEntitiesGet, map[string]string{"id": ids[key]})
		if visible && he != nil || !visible && (he == nil || he.Code != sdk.ErrNotFound) {
			t.Errorf("fetch entities.get %s: %v (visible=%v)", key, he, visible)
		}
	}
	// A list never carries the hidden ones.
	raw, he := callAs(t, a, withPhase(rc, sdk.RunPhaseFetch), sdk.FnEntitiesList, map[string]any{"limit": 200})
	if he != nil {
		t.Fatal(he)
	}
	var list struct{ Items []struct{ ID string } }
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	for _, it := range list.Items {
		for _, hidden := range []string{"draft", "variant", "foreign-locale", "hidden-person"} {
			if it.ID == ids[hidden] {
				t.Errorf("fetch entities.list returned %s", hidden)
			}
		}
	}
	// Media: only what a public page uses.
	if he := get(sdk.RunPhaseFetch, sdk.FnMediaGet, map[string]string{"id": mediaIDs["used"]}); he != nil {
		t.Errorf("fetch media.get used: %v", he)
	}
	if he := get(sdk.RunPhaseFetch, sdk.FnMediaGet, map[string]string{"id": mediaIDs["unused"]}); he == nil || he.Code != sdk.ErrNotFound {
		t.Errorf("fetch media.get unused: %v", he)
	}
	raw, he = callAs(t, a, withPhase(rc, sdk.RunPhaseFetch), sdk.FnMediaList, map[string]any{"limit": 200})
	if he != nil {
		t.Fatal(he)
	}
	var ml struct{ Items []struct{ ID string } }
	_ = json.Unmarshal(raw, &ml)
	for _, it := range ml.Items {
		if it.ID == mediaIDs["unused"] {
			t.Error("fetch media.list returned an unused file")
		}
	}
	// Transform still reads every published entity, and http.fetch is a
	// different phase's business: this step just proves the fetch narrowing
	// does not leak into transform.
	if he := get(sdk.RunPhaseTransform, sdk.FnEntitiesGet, map[string]string{"id": ids["hidden-person"]}); he != nil {
		t.Errorf("transform entities.get hidden person: %v", he)
	}
	if he := get(sdk.RunPhaseTransform, sdk.FnEntitiesGet, map[string]string{"id": ids["draft"]}); he == nil {
		t.Error("transform must not read a draft")
	}
}

func withPhase(rc RunCtx, phase string) RunCtx { rc.Phase = phase; return rc }
