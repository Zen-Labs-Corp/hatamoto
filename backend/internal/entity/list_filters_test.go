package entity

import (
	"testing"

	"github.com/google/uuid"
)

func TestAppendListFilters(t *testing.T) {
	sid := uuid.New()
	tid := uuid.New()
	st := StatusPublished
	cases := []struct {
		name   string
		filter *ListEntitiesFilter
		want   string
		args   int
	}{
		{"nil", nil, "", 0},
		{"empty", &ListEntitiesFilter{}, "", 0},
		{"base", &ListEntitiesFilter{SchemaID: &sid, Status: &st, Search: "seo"}, " AND e.schema_id = $1 AND e.status = $2 AND (e.name ILIKE $3 OR e.slug ILIKE $3)", 3},
		{"exclude", &ListEntitiesFilter{ExcludeSchemaIDs: []uuid.UUID{sid, uuid.New()}, Status: &st}, " AND e.schema_id <> ALL($1::uuid[]) AND e.status = $2", 2},
		{"term exact", &ListEntitiesFilter{TermID: &tid, TermExact: true}, " AND EXISTS (SELECT 1 FROM hatamoto_entity_terms et WHERE et.entity_id = e.id AND et.term_id = $1)", 1},
		{"term with descendants", &ListEntitiesFilter{TermID: &tid}, " AND EXISTS (\n\t\t\tWITH RECURSIVE sub(id) AS (\n\t\t\t\tSELECT id FROM hatamoto_terms WHERE id = $1\n\t\t\t\tUNION ALL\n\t\t\t\tSELECT t.id FROM hatamoto_terms t JOIN sub ON t.parent_id = sub.id\n\t\t\t)\n\t\t\tSELECT 1 FROM hatamoto_entity_terms et WHERE et.entity_id = e.id AND et.term_id IN (SELECT id FROM sub))", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, args, next := appendListFilters(c.filter, nil, 1)
			if got != c.want {
				t.Fatalf("where = %q, want %q", got, c.want)
			}
			if len(args) != c.args || next != c.args+1 {
				t.Fatalf("args=%d next=%d, want %d/%d", len(args), next, c.args, c.args+1)
			}
		})
	}
}
