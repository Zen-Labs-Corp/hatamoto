package entity

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestFKViolationUsesConstraintName(t *testing.T) {
	pg := func(c string) error {
		return fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "23503", ConstraintName: c, Message: "mentions author and parent"})
	}
	for c, want := range map[string]error{
		"hatamoto_entities_author_fkey":    ErrAuthorNotFound,
		"hatamoto_entities_parent_id_fkey": ErrParentNotFound,
		"hatamoto_entities_schema_id_fkey": ErrSchemaNotFound,
	} {
		if got := fkViolation(pg(c)); got != want {
			t.Errorf("%s -> %v, want %v", c, got, want)
		}
	}
}

func TestSlugConflictSaysWhichRule(t *testing.T) {
	flat := slugConflict(&pgconn.PgError{Code: "23505", ConstraintName: "uq_entities_schema_slug"})
	nested := slugConflict(&pgconn.PgError{Code: "23505", ConstraintName: "uq_entities_schema_parent_slug"})
	if !errors.Is(flat, ErrSlugAlreadyExists) || !errors.Is(nested, ErrSlugAlreadyExists) || flat.Error() == nested.Error() {
		t.Errorf("flat = %v, nested = %v", flat, nested)
	}
	for _, err := range []error{flat, nested} {
		if !strings.Contains(err.Error(), "locale") {
			t.Errorf("%v does not mention the locale", err)
		}
	}
}

func TestSchemaAllowsProperty(t *testing.T) {
	for schema, want := range map[string]bool{
		`{"type":"object"}`:                              true,
		`{"type":"object","additionalProperties":true}`:  true,
		`{"type":"object","additionalProperties":false}`: false,
		`{"type":"object","additionalProperties":false,"properties":{"author":{"type":"string"}}}`: true,
		`not json`: true,
	} {
		if got := schemaAllowsProperty(json.RawMessage(schema), "author"); got != want {
			t.Errorf("%s -> %v, want %v", schema, got, want)
		}
	}
}
