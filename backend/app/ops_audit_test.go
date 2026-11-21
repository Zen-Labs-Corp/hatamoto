package app

import (
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/audit"
)

func TestCliAuditActorWithoutAs(t *testing.T) {
	t.Parallel()

	a := cliActor("", nil)
	if a.Name != "cli" || a.Kind != audit.KindCLI || a.Via != "cli" || a.ID != "" {
		t.Fatalf("actor = %+v", a)
	}
}

func TestCliAuditActorWithAs(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	a := cliActor(" ana@example.test ", &id)
	if a.Name != "ana@example.test" || a.ID != id.String() || a.Kind != audit.KindCLI || a.Via != "cli" {
		t.Fatalf("actor = %+v", a)
	}
}
