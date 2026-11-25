package entity

import (
	"context"
	"encoding/json/jsontext"
	"testing"

	"github.com/google/uuid"
)

// summaryRecorder keeps the change summary of the last Update, which the plain
// mock drops.
type summaryRecorder struct {
	*mockEntityRepository
	summary string
}

func (r *summaryRecorder) Update(ctx context.Context, id uuid.UUID, userID *uuid.UUID, in *UpdateEntityInput) (*Entity, error) {
	if in.ChangeSummary != nil {
		r.summary = *in.ChangeSummary
	}
	return r.mockEntityRepository.Update(ctx, id, userID, in)
}

func TestRestoreVersionIfSummaryTag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opts []RestoreOption
		want string
	}{
		{"no option keeps the default summary", nil, "Restored from version 1"},
		{"tag prefixes the summary", []RestoreOption{RestoreSummaryTag("[mcp:k]")}, "[mcp:k] Restored from version 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sr, mock := newMockSchemaRepository(), newMockEntityRepository()
			rec := &summaryRecorder{mockEntityRepository: mock}
			svc := NewService(sr, rec, testLogger())
			e := seedEntity(t, mock, seedSchema(t, sr))
			mock.versions[e.ID] = []*EntityVersion{{ID: uuid.New(), EntityID: e.ID, Version: 1, Attributes: jsontext.Value(`{"title":"Old","body":"x"}`)}}

			if _, err := svc.RestoreVersionIf(context.Background(), e.ID, &testUserID, 1, nil, tt.opts...); err != nil {
				t.Fatal(err)
			}
			if rec.summary != tt.want {
				t.Fatalf("summary = %q, want %q", rec.summary, tt.want)
			}
		})
	}
}
