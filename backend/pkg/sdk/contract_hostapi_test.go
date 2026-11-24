package sdk

import (
	jsonv2 "encoding/json/v2"
	"slices"
	"testing"
)

func TestContractHostFunctionNames(t *testing.T) {
	for got, want := range map[string]string{
		FnEntitiesCreate:    "entities.create",
		FnEntitiesUpdate:    "entities.update",
		FnEntitiesDelete:    "entities.delete",
		FnCommentsMarkDead:  "comments.mark_dead",
		FnCommentsRestore:   "comments.restore",
		FnCommentsSetRank:   "comments.set_rank",
		FnCommentsSetViewer: "comments.set_viewer",
		FnCommentsByAuthor:  "comments.by_author",
		FnCommentsSubtree:   "comments.subtree",
	} {
		if got != want {
			t.Errorf("host function %q, want %q", got, want)
		}
	}
	if MaxCommentIDs != 100 || MaxCommentRankItems != 200 {
		t.Errorf("limits = %d, %d, want 100, 200", MaxCommentIDs, MaxCommentRankItems)
	}
}

func TestContractArgKeys(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want []string
	}{
		{EntityWrite{ID: "i", Name: "n", Slug: "s", Locale: "l", Attributes: []byte(`{"a":1}`), Status: "draft"}, []string{"attributes", "id", "locale", "name", "slug", "status"}},
		{EntityDeleteArgs{ID: "i"}, []string{"id"}},
		{CommentsThreadArgs{EntityID: "e", Page: 1, IncludeDead: true}, []string{"entity_id", "include_dead", "page"}},
		{CommentIDsArgs{IDs: []string{"a"}}, []string{"ids"}},
		{CommentRankItem{ID: "a", Rank: 1.5}, []string{"id", "rank"}},
		{CommentRankArgs{Items: []CommentRankItem{{ID: "a"}}}, []string{"items"}},
		{CommentsChanged{Changed: 1}, []string{"changed"}},
		{CommentViewerArgs{CommenterKey: "k", ShowDead: true}, []string{"commenter_key", "show_dead"}},
		{CommentsByAuthorArgs{CommenterKey: "k", Page: 1, IncludeDead: true}, []string{"commenter_key", "include_dead", "page"}},
		{CommentSubtreeArgs{CommentID: "c", Page: 1, IncludeDead: true}, []string{"comment_id", "include_dead", "page"}},
		{HTTPRequest{CommenterKey: "c", AddrKey: "a", NetKey: "n"}, []string{"addr_key", "commenter_key", "method", "net_key", "path"}},
	} {
		b, err := jsonv2.Marshal(tc.v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := jsonv2.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		var got []string
		for k := range m {
			got = append(got, k)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%T keys = %v, want %v", tc.v, got, tc.want)
		}
	}
}
