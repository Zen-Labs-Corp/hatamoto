package admin

import (
	"testing"

	"github.com/google/uuid"
)

func TestOrderTermsManualThenAlphabetical(t *testing.T) {
	root := uuid.New()
	nodes := []termNode{
		{ID: uuid.New(), Kind: "tag", Name: "beta"},
		{ID: uuid.New(), Kind: "tag", Name: "Alpha"},
		{ID: uuid.New(), Kind: "tag", Name: "gamma", SortOrder: 1},
		{ID: root, Kind: "tag", Name: "zulu", SortOrder: 2},
		{ID: uuid.New(), ParentID: &root, Kind: "tag", Name: "child-b", SortOrder: 2},
		{ID: uuid.New(), ParentID: &root, Kind: "tag", Name: "child-a", SortOrder: 3},
		{ID: uuid.New(), ParentID: &root, Kind: "tag", Name: "child-c", SortOrder: 1},
	}
	var names []string
	for _, n := range orderTerms(nodes) {
		names = append(names, n.Name)
	}
	// Unordered terms (0) first, alphabetical; then the ordered ones by position,
	// each followed by its children in their own order.
	want := []string{"Alpha", "beta", "gamma", "zulu", "child-c", "child-b", "child-a"}
	for i := range want {
		if i >= len(names) || names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}

func TestOrderTermsAlphabeticalWhileAllZero(t *testing.T) {
	nodes := []termNode{
		{ID: uuid.New(), Kind: "tag", Name: "b"},
		{ID: uuid.New(), Kind: "tag", Name: "A"},
		{ID: uuid.New(), Kind: "tag", Name: "c"},
	}
	var names []string
	for _, n := range orderTerms(nodes) {
		names = append(names, n.Name)
	}
	if names[0] != "A" || names[1] != "b" || names[2] != "c" {
		t.Errorf("order = %v", names)
	}
}
