package entity

import (
	"testing"

	"github.com/google/uuid"
)

func TestAppendListFilters(t *testing.T) {
	sid := uuid.New()
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
