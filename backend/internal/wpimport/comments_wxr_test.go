package wpimport

import (
	_ "embed"
	"reflect"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/jsonx"
)

//go:embed testdata/comments.wxr.xml
var commentsWXR string

func parseCommentsFixture(t *testing.T) *Dump {
	t.Helper()
	d, err := ParseWXR(strings.NewReader(commentsWXR), WXROptions{})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCommentsParsed(t *testing.T) {
	d := parseCommentsFixture(t)
	if len(d.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(d.Items))
	}
	first, second := d.Items[0], d.Items[1]
	if len(first.Comments) != 13 || len(second.Comments) != 5 {
		t.Fatalf("comments = %d, %d; want 13, 5", len(first.Comments), len(second.Comments))
	}
	if d.CommentsDropped != 2 {
		t.Errorf("CommentsDropped = %d, want 2 (the trashed post's)", d.CommentsDropped)
	}
	byID := map[string]Comment{}
	for _, c := range first.Comments {
		byID[c.ID] = c
	}
	for id, parent := range map[string]string{"1": "0", "2": "1", "3": "2", "10": "999"} {
		if byID[id].ParentID != parent {
			t.Errorf("comment %s parent = %q, want %q", id, byID[id].ParentID, parent)
		}
	}
	for id, st := range map[string]string{"1": "1", "4": "0", "5": "spam", "6": "trash", "7": "post-trashed"} {
		if byID[id].Approved != st {
			t.Errorf("comment %s approved = %q, want %q", id, byID[id].Approved, st)
		}
	}
	if byID["8"].Type != "pingback" || byID["9"].Type != "trackback" || byID["1"].Type != "" {
		t.Errorf("types = %q %q %q", byID["8"].Type, byID["9"].Type, byID["1"].Type)
	}
	if got := byID["2"].Date; !got.Equal(time.Date(2024, 6, 2, 11, 30, 0, 0, time.UTC)) || got.Location() != time.UTC {
		t.Errorf("date = %v", got)
	}
	if byID["2"].AuthorName != "Bob Sample" || byID["2"].AuthorEmail != "bob@example.test" || byID["2"].IP != "203.0.113.77" || !byID["2"].HadURL {
		t.Errorf("comment 2 = %+v", byID["2"])
	}
	if byID["12"].AuthorEmail != "" {
		t.Errorf("whitespace email kept as %q", byID["12"].AuthorEmail)
	}
	if byID["13"].Content != "<p></p>" {
		t.Errorf("content = %q", byID["13"].Content)
	}
	// gmt date is zero: fall back to comment_date.
	for _, c := range second.Comments {
		if c.ID == "30" && !c.Date.Equal(time.Date(2024, 6, 5, 10, 0, 0, 0, time.UTC)) {
			t.Errorf("fallback date = %v", c.Date)
		}
	}
}

func TestCommentsScanCounts(t *testing.T) {
	r := Scan(parseCommentsFixture(t), "test")
	if r.Comments != 16 || r.CommentPings != 2 {
		t.Errorf("Comments = %d, CommentPings = %d; want 16, 2", r.Comments, r.CommentPings)
	}
}

func TestCommentHasNoURLField(t *testing.T) {
	typ := reflect.TypeOf(Comment{})
	for i := range typ.NumField() {
		n := typ.Field(i).Name
		if strings.Contains(strings.ToUpper(n), "URL") && n != "HadURL" {
			t.Errorf("Comment has field %s", n)
		}
	}
}

func TestCommentsNotMarshalled(t *testing.T) {
	d := parseCommentsFixture(t)
	b, err := jsonx.Marshal(d.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"203.0.113.77", "example.test/\"", "bob@example.test", "ann@example.test", "url-sentinel"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("marshalled item contains %q", leak)
		}
	}
	if !strings.Contains(string(b), "Bob Sample") {
		t.Error("marshalled item lacks comments")
	}
}
