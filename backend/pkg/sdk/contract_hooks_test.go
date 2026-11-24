package sdk

import (
	"slices"
	"testing"
)

func TestContractHookNames(t *testing.T) {
	if got := CommentUpdated.Export(); got != "on_comment_updated" {
		t.Errorf("CommentUpdated export = %q", got)
	}
	if got := CommentDraftF.Export(); got != "filter_comment_draft" {
		t.Errorf("CommentDraftF export = %q", got)
	}
	if !slices.Contains(ActionNames, "comment.updated") {
		t.Errorf("ActionNames = %v, missing comment.updated", ActionNames)
	}
	if !slices.Contains(FilterNames, "comment.draft") {
		t.Errorf("FilterNames = %v, missing comment.draft", FilterNames)
	}
}

func TestContractHookTimeout(t *testing.T) {
	m := Manifest{Name: "pager", Version: "0.1.0", SDK: "^0.27", Limits: Limits{HookTimeoutsMS: map[string]uint64{"comment.draft": 300}}}
	if err := m.Validate(); err != nil {
		t.Fatalf("comment.draft timeout refused: %v", err)
	}
}
