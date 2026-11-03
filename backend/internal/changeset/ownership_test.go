package changeset

import (
	"context"
	"errors"
	"testing"
)

func TestOrigin(t *testing.T) {
	if got := Origin(""); got != "owner" {
		t.Errorf("Origin(\"\") = %q", got)
	}
	if got := Origin("seo-tools"); got != "plugin:seo-tools" {
		t.Errorf("Origin = %q", got)
	}
}

// A singleton kind is refused before the engine touches the database.
func TestStageRefusesSingletonKinds(t *testing.T) {
	e := &Engine{}
	for k := range singletonKinds {
		err := e.Stage(context.Background(), 1, Item{Kind: k, Op: OpUpsert})
		if !errors.Is(err, ErrSingleton) {
			t.Errorf("kind %q: err = %v, want ErrSingleton", k, err)
		}
	}
}

func TestEveryKindHasAnOriginTable(t *testing.T) {
	for _, k := range []Kind{KindEntity, KindTerm, KindMenu, KindRedirect, KindMedia} {
		if originTables[k] == "" {
			t.Errorf("no origin table for %q", k)
		}
	}
}
