//go:build integration

package person

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"hatamoto.dev/core/internal/redirect"
)

type recordedMoves struct{ moves []redirect.PathMove }

func (r *recordedMoves) MoveEntityPaths(_ context.Context, m []redirect.PathMove) error {
	r.moves = append(r.moves, m...)
	return nil
}

// Needs a database migrated to head (DATABASE_URL); everything runs in a
// transaction that is rolled back, the merge in a savepoint inside it.
func TestMerge(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(tx)
	byline := NewByline(tx)
	pathOf := func(_ context.Context, slug string) string { return "/author/" + slug }

	person := func(name string, public bool) *Person {
		t.Helper()
		b, err := byline.Resolve(ctx, name)
		must(err)
		_, err = tx.Exec(ctx, `UPDATE hatamoto_entities SET status = 'published', attributes = jsonb_set(attributes, '{public}', to_jsonb($2::bool)) WHERE id = $1`, *b.PersonID, public)
		must(err)
		p, err := store.Get(ctx, *b.PersonID)
		must(err)
		return p
	}
	post := func(slug string, p *Person) uuid.UUID {
		t.Helper()
		mirror, err := byline.Mirror(ctx, p.ID)
		must(err)
		var id uuid.UUID
		must(tx.QueryRow(ctx, `
			INSERT INTO hatamoto_entities (schema_id, name, slug, status, attributes, byline_person_id, author_id)
			SELECT id, $1::text, $1::text, 'published', jsonb_build_object('author', $2::text), $3, $4 FROM hatamoto_entity_schemas WHERE slug = 'post'
			RETURNING id`, slug, p.Name, p.ID, *mirror).Scan(&id))
		return id
	}
	user := func(email string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		must(tx.QueryRow(ctx, `INSERT INTO hatamoto_users (id, email, password_hash, name) VALUES (gen_random_uuid(), $1, 'x', 'U') RETURNING id`, email).Scan(&id))
		return id
	}

	// Posts move, the login moves, the drop is gone, a 301 is written.
	keep, drop := person("Alex Carter", true), person("Alex C", true)
	p1, p2 := post("mg-post-1", drop), post("mg-post-2", keep)
	u := user("mg-a@example.com")
	must(store.Link(ctx, u, drop.ID))
	rec := &recordedMoves{}
	res, err := store.Merge(ctx, keep.ID, drop.ID, pathOf, rec)
	must(err)
	if res.Posts != 1 || res.Keep.ID != keep.ID || res.Drop.ID != drop.ID {
		t.Errorf("result = %+v", res)
	}
	keepMirror, err := byline.Mirror(ctx, keep.ID)
	must(err)
	var credit uuid.UUID
	var author string
	var mirror uuid.UUID
	must(tx.QueryRow(ctx, `SELECT byline_person_id, author_id, attributes->>'author' FROM hatamoto_entities WHERE id = $1`, p1).Scan(&credit, &mirror, &author))
	if credit != keep.ID || mirror != *keepMirror || author != "Alex Carter" {
		t.Errorf("reassigned post: person %v mirror %v author %q", credit, mirror, author)
	}
	must(tx.QueryRow(ctx, `SELECT byline_person_id FROM hatamoto_entities WHERE id = $1`, p2).Scan(&credit))
	if credit != keep.ID {
		t.Error("a post of the kept person changed")
	}
	if _, err := store.Get(ctx, drop.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("dropped person still there: %v", err)
	}
	if linked, err := store.LinkedUser(ctx, keep.ID); err != nil || linked != u {
		t.Errorf("login after merge = %v, %v; want it on the kept person", linked, err)
	}
	if len(rec.moves) != 1 || rec.moves[0].From != "/author/"+drop.Slug || rec.moves[0].To != "/author/"+keep.Slug {
		t.Errorf("redirects = %+v", rec.moves)
	}

	// A hidden drop has no public URL to redirect.
	keep2, drop2 := person("Mg Keep Two", true), person("Mg Drop Two", false)
	rec = &recordedMoves{}
	_, err = store.Merge(ctx, keep2.ID, drop2.ID, pathOf, rec)
	must(err)
	if len(rec.moves) != 0 {
		t.Errorf("a hidden person got a redirect: %+v", rec.moves)
	}

	// Two people with a login are refused and nothing changes.
	k3, d3 := person("Mg Keep Three", true), person("Mg Drop Three", true)
	must(store.Link(ctx, user("mg-k3@example.com"), k3.ID))
	must(store.Link(ctx, user("mg-d3@example.com"), d3.ID))
	post("mg-post-3", d3)
	if _, err := store.Merge(ctx, k3.ID, d3.ID, pathOf, &recordedMoves{}); !errors.Is(err, ErrMergeBothLogins) {
		t.Errorf("both logins = %v", err)
	}
	if _, err := store.Get(ctx, d3.ID); err != nil {
		t.Errorf("a refused merge deleted the person: %v", err)
	}
	var n int
	must(tx.QueryRow(ctx, `SELECT count(*) FROM hatamoto_entities WHERE byline_person_id = $1`, d3.ID).Scan(&n))
	if n != 1 {
		t.Errorf("after a refused merge the dropped person has %d posts, want 1", n)
	}

	// The kept person's login stays when the dropped one has none.
	k4, d4 := person("Mg Keep Four", true), person("Mg Drop Four", true)
	u4 := user("mg-k4@example.com")
	must(store.Link(ctx, u4, k4.ID))
	if _, err := store.Merge(ctx, k4.ID, d4.ID, pathOf, nil); err != nil {
		t.Fatal(err)
	}
	if linked, err := store.LinkedUser(ctx, k4.ID); err != nil || linked != u4 {
		t.Errorf("kept login = %v, %v", linked, err)
	}

	// The profile is not lost: the kept person gets the fields it lacks, the
	// login moves, and what keep already has wins.
	k5, d5 := person("Mg Keep Five", false), person("Mg Drop Five", true)
	_, err = tx.Exec(ctx, `UPDATE hatamoto_entities SET attributes = attributes || '{"job_title":"Lead","photo":"","on_team":false}'::jsonb WHERE id = $1`, k5.ID)
	must(err)
	_, err = tx.Exec(ctx, `UPDATE hatamoto_entities SET attributes = attributes || '{"job_title":"Intern","photo":"/m/d5.jpg","short_bio":"Hi","on_team":true,"linkedin":"https://linkedin.com/in/d5"}'::jsonb WHERE id = $1`, d5.ID)
	must(err)
	u5 := user("mg-d5@example.com")
	must(store.Link(ctx, u5, d5.ID))
	if _, err := store.Merge(ctx, k5.ID, d5.ID, pathOf, nil); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, k5.ID)
	must(err)
	if got.Photo != "/m/d5.jpg" || got.ShortBio != "Hi" || got.Links["linkedin"] == "" {
		t.Errorf("kept person lost the dropped profile: %+v", got)
	}
	if got.JobTitle != "Lead" || got.Public || got.OnTeam {
		t.Errorf("keep's own values must win: %+v", got)
	}
	if linked, err := store.LinkedUser(ctx, k5.ID); err != nil || linked != u5 {
		t.Errorf("login after merge = %v, %v; want it on the kept person", linked, err)
	}

	// A hidden person's details never reach a public person; a hidden keep
	// still receives them (and stays hidden).
	k6, d6 := person("Mg Keep Six", true), person("Mg Drop Six", false)
	_, err = tx.Exec(ctx, `UPDATE hatamoto_entities SET attributes = attributes || '{"short_bio":"Private","job_title":"Secret"}'::jsonb WHERE id = $1`, d6.ID)
	must(err)
	if _, err := store.Merge(ctx, k6.ID, d6.ID, pathOf, nil); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, k6.ID)
	must(err)
	if !got.Public || got.ShortBio != "" || got.JobTitle != "" {
		t.Errorf("public keep took a hidden person's details: %+v", got)
	}
	k7, d7 := person("Mg Keep Seven", false), person("Mg Drop Seven", false)
	_, err = tx.Exec(ctx, `UPDATE hatamoto_entities SET attributes = attributes || '{"short_bio":"Private"}'::jsonb WHERE id = $1`, d7.ID)
	must(err)
	if _, err := store.Merge(ctx, k7.ID, d7.ID, pathOf, nil); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, k7.ID)
	must(err)
	if got.Public || got.ShortBio != "Private" {
		t.Errorf("hidden keep after merging a hidden person: %+v", got)
	}

	// Edge cases.
	if _, err := store.Merge(ctx, keep.ID, keep.ID, pathOf, nil); !errors.Is(err, ErrMergeSame) {
		t.Errorf("same person = %v", err)
	}
	if _, err := store.Merge(ctx, keep.ID, uuid.New(), pathOf, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown person = %v", err)
	}
}
