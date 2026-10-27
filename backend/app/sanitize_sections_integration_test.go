//go:build integration

package app

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// sanitize-html-sections covers entities in one
// transaction, with a --dry-run that changes nothing.
// DATABASE_URL=... go test -tags=integration ./app -run SanitizeHTMLSections
func TestSanitizeHTMLSections_Integration(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	suffix := uuid.NewString()[:6]
	schemaID, dirtyEnt, cleanEnt := uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO hatamoto_entity_schemas (id,name,slug,schema,settings) VALUES ($1,$2,$2,'{"type":"object"}','{}')`, schemaID, "it-san-"+suffix)
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, `DELETE FROM hatamoto_entities WHERE schema_id = $1`, schemaID)
		_, _ = conn.Exec(ctx, `DELETE FROM hatamoto_entity_schemas WHERE id = $1`, schemaID)
	})

	section := func(body string) string {
		return `{"page_sections":[{"type":"html","content":{"body":"` + body + `"}},{"type":"hero","content":{"body":"<b onclick=\"x()\">keep</b>"}}]}`
	}
	dirty := section(`<p>hi</p><button onclick=\"go()\">b</button><script>x</script>`)
	clean := section(`<p>fine</p>`)
	insEnt := func(id uuid.UUID, slug, attrs string) {
		exec(`INSERT INTO hatamoto_entities (id,schema_id,name,slug,status,attributes) VALUES ($1,$2,$3,$3,'draft',$4::jsonb)`, id, schemaID, slug, attrs)
	}
	insEnt(dirtyEnt, "dirty-"+suffix, dirty)
	insEnt(cleanEnt, "clean-"+suffix, clean)

	text := func(q string, args ...any) string {
		var s string
		if err := conn.QueryRow(ctx, q, args...).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	entAttrs := func(id uuid.UUID) string {
		return text(`SELECT attributes::text FROM hatamoto_entities WHERE id=$1`, id)
	}
	isDirty := func(s string) bool { return strings.Contains(s, "onclick=\\\"go()") || strings.Contains(s, "<script>") }
	beforeCleanEnt := entAttrs(cleanEnt)

	var out bytes.Buffer
	if err := runSanitizeHTMLSections(ctx, dsn, true, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"entities would update:", "html sections would update (entities):"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run report lacks %q:\n%s", want, out.String())
		}
	}
	if !isDirty(entAttrs(dirtyEnt)) {
		t.Fatal("dry-run changed data")
	}

	out.Reset()
	if err := runSanitizeHTMLSections(ctx, dsn, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "entities updated:") {
		t.Fatalf("report:\n%s", out.String())
	}
	if isDirty(entAttrs(dirtyEnt)) {
		t.Fatalf("html still dirty: %s", entAttrs(dirtyEnt))
	}
	if !strings.Contains(entAttrs(dirtyEnt), "<p>hi</p>") {
		t.Fatalf("body lost: %s", entAttrs(dirtyEnt))
	}
	// Non-html sections are never touched; clean rows are byte-identical.
	if !strings.Contains(entAttrs(dirtyEnt), `onclick=\"x()\"`) {
		t.Fatal("non-html section was rewritten")
	}
	if entAttrs(cleanEnt) != beforeCleanEnt {
		t.Fatal("clean rows were rewritten")
	}
}
