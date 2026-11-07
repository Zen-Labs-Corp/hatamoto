//go:build integration

package form_test

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/form"
)

func TestSaveDefinitionVersion(t *testing.T) {
	ctx := context.Background()
	repo := form.NewPostgresRepository(testDB(t))
	f, err := repo.CreateForm(ctx, &form.CreateFormInput{Name: "Contact", Fields: []form.FormField{}})
	if err != nil {
		t.Fatal(err)
	}
	in := &form.SaveDefinitionInput{
		Name: "Contact 2", Status: form.StatusActive,
		Fields: []form.FormField{{ID: "a", Type: form.FieldTypeText, Name: "a", Label: "A"}},
	}

	// The version survives a string round trip at microsecond precision.
	base, err := form.ParseVersion(f.Version())
	if err != nil || !base.Equal(f.UpdatedAt) {
		t.Fatalf("version round trip: %v %v vs %v", base, err, f.UpdatedAt)
	}

	first, err := repo.SaveDefinition(ctx, f.ID, base, in)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}
	if first.Name != "Contact 2" || len(first.Fields) != 1 || first.Version() == f.Version() {
		t.Fatalf("first save = %+v", first)
	}
	if _, err := repo.SaveDefinition(ctx, f.ID, base, in); !errors.Is(err, form.ErrVersionConflict) {
		t.Fatalf("second save with the same base: err = %v, want ErrVersionConflict", err)
	}

	next, err := form.ParseVersion(first.Version())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveDefinition(ctx, f.ID, next, in); err != nil {
		t.Fatalf("save with the current version: %v", err)
	}
}
