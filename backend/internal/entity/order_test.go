package entity

import "testing"

func TestOrderSQLManual(t *testing.T) {
	arg := func(any) string { return "$1" }
	for spec, want := range map[string]string{
		"manual":  "e.sort_order ASC, e.created_at ASC, e.id ASC",
		"-manual": "e.sort_order DESC, e.created_at DESC, e.id DESC",
		"":        "e.published_at DESC NULLS LAST, e.created_at DESC, e.id",
		"bogus":   "e.published_at DESC NULLS LAST, e.created_at DESC, e.id",
	} {
		if got := orderSQL(spec, arg); got != want {
			t.Errorf("orderSQL(%q) = %q, want %q", spec, got, want)
		}
	}
}

func TestListOrderManualIgnoresDirection(t *testing.T) {
	want := "ORDER BY e.parent_id NULLS FIRST, e.sort_order, e.created_at, e.id"
	for _, asc := range []bool{false, true} {
		if got := listOrder(&ListEntitiesFilter{Sort: "manual", Asc: asc}); got != want {
			t.Errorf("listOrder(manual, asc=%v) = %q", asc, got)
		}
	}
}
