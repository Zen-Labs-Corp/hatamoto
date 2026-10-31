package entity

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOriginalOf(t *testing.T) {
	g := uuid.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	en := Member{ID: uuid.New(), Group: g, Locale: "en", CreatedAt: t0.Add(time.Hour)}
	fr := Member{ID: uuid.New(), Group: g, Locale: "fr", CreatedAt: t0}
	it := Member{ID: uuid.New(), Group: g, Locale: "it", CreatedAt: t0.Add(2 * time.Hour)}

	flagged := it
	flagged.Original = true
	if got := OriginalOf([]Member{en, fr, flagged}, "en"); got.ID != it.ID {
		t.Errorf("flagged: got %s, want it", got.Locale)
	}
	if got := OriginalOf([]Member{fr, en, it}, "en"); got.ID != en.ID {
		t.Errorf("default locale: got %s, want en", got.Locale)
	}
	if got := OriginalOf([]Member{en, fr, it}, "pt"); got.ID != fr.ID {
		t.Errorf("oldest: got %s, want fr", got.Locale)
	}
	loose := fr
	loose.Group, loose.Original = uuid.Nil, true
	if got := OriginalOf([]Member{en, loose}, "en"); got.ID != en.ID {
		t.Errorf("flag without group: got %s, want en", got.Locale)
	}
	if OriginalOf(nil, "en") != nil {
		t.Error("no members: want nil")
	}
}

func TestOutOfDate(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	orig := Member{ID: uuid.New(), ContentChangedAt: t0.Add(time.Hour)}
	older := Member{ID: uuid.New(), ContentChangedAt: t0}
	same := Member{ID: uuid.New(), ContentChangedAt: orig.ContentChangedAt}
	newer := Member{ID: uuid.New(), ContentChangedAt: t0.Add(2 * time.Hour)}
	if !OutOfDate(older, orig) {
		t.Error("older copy: want out of date")
	}
	if OutOfDate(same, orig) {
		t.Error("equal time: want not out of date")
	}
	if OutOfDate(newer, orig) {
		t.Error("newer copy: want not out of date")
	}
	if OutOfDate(orig, orig) {
		t.Error("the original is never out of date")
	}
}
