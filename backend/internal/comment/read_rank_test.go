package comment

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRankOrder(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	mk := func(parent *ThreadComment, rank float64, age int) ThreadComment {
		n++
		c := ThreadComment{ID: uuid.New(), Rank: rank, CreatedAt: base.Add(time.Duration(age) * time.Minute), Depth: 1}
		if parent != nil {
			c.ParentID, c.Depth = &parent.ID, parent.Depth+1
		}
		return c
	}
	r1, r2, r3 := mk(nil, 5, 1), mk(nil, 1, 2), mk(nil, 3, 3) // input root order is kept
	a := mk(&r1, 1, 10)
	b := mk(&r1, 9, 11)
	c := mk(&r1, 9, 12) // ties b on rank, newer first
	a1 := mk(&a, 2, 20)
	a2 := mk(&a, 7, 21)
	d := mk(&r3, 0, 30)
	in := []ThreadComment{r1, a, a1, a2, b, c, r2, r3, d}
	got := rankOrder(in)
	want := []ThreadComment{r1, c, b, a, a2, a1, r2, r3, d}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i].ID {
			t.Errorf("position %d = rank %v age %v, want rank %v age %v", i, got[i].Rank, got[i].CreatedAt, want[i].Rank, want[i].CreatedAt)
		}
	}
}
