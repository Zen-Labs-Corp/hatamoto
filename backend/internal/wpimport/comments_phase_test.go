package wpimport

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/jsonx"
)

func TestMapWPStatus(t *testing.T) {
	for in, want := range map[string]struct {
		st    comment.Status
		known bool
	}{
		"1":            {comment.StatusApproved, true},
		"0":            {comment.StatusPending, true},
		"spam":         {comment.StatusSpam, true},
		"trash":        {comment.StatusTrash, true},
		"post-trashed": {comment.StatusTrash, true},
		"":             {comment.StatusPending, false},
		"weird":        {comment.StatusPending, false},
	} {
		st, known := mapWPStatus(in)
		if st != want.st || known != want.known {
			t.Errorf("mapWPStatus(%q) = %q, %v; want %q, %v", in, st, known, want.st, want.known)
		}
	}
}

func ids(cs []Comment) string {
	var b []string
	for _, c := range cs {
		b = append(b, c.ID)
	}
	return strings.Join(b, ",")
}

func TestOrderThread(t *testing.T) {
	cases := map[string]struct {
		in   []Comment
		want string
	}{
		"children before parents": {[]Comment{{ID: "3", ParentID: "2"}, {ID: "2", ParentID: "1"}, {ID: "1", ParentID: "0"}}, "1,2,3"},
		"orphan stays":            {[]Comment{{ID: "5", ParentID: "999"}, {ID: "6", ParentID: "5"}}, "5,6"},
		"source order kept":       {[]Comment{{ID: "b"}, {ID: "a"}, {ID: "c", ParentID: "a"}}, "b,a,c"},
		"cycle terminates":        {[]Comment{{ID: "1", ParentID: "2"}, {ID: "2", ParentID: "1"}, {ID: "3"}}, ""},
	}
	for name, c := range cases {
		got := orderThread(c.in)
		if name == "cycle terminates" {
			if len(got) != 3 {
				t.Errorf("%s: %s", name, ids(got))
			}
			continue
		}
		if ids(got) != c.want {
			t.Errorf("%s: got %s, want %s", name, ids(got), c.want)
		}
	}
	var deep []Comment
	for i := 200; i >= 1; i-- {
		p := "0"
		if i > 1 {
			p = strconv.Itoa(i - 1)
		}
		deep = append(deep, Comment{ID: strconv.Itoa(i), ParentID: p})
	}
	if got := orderThread(deep); len(got) != 200 {
		t.Errorf("deep chain: %d comments, want 200", len(got))
	}
}

func TestUsableEmail(t *testing.T) {
	for in, want := range map[string]bool{"a@example.test": true, " A@Example.test ": true, "": false, "   ": false, "nobody": false} {
		if got := usableEmail(in); got != want {
			t.Errorf("usableEmail(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPlainCommentTypes(t *testing.T) {
	for in, want := range map[string]bool{"": true, "comment": true, "pingback": false, "trackback": false, "review": false} {
		if isPlainComment(in) != want {
			t.Errorf("isPlainComment(%q) = %v", in, !want)
		}
	}
}

func TestEmptyBodiesAreRefused(t *testing.T) {
	_, err := commentfmt.Render(commentHTMLToText("<p> </p>"))
	if !errors.Is(err, commentfmt.ErrEmpty) {
		t.Errorf("err = %v, want ErrEmpty", err)
	}
	_, err = commentfmt.Render(commentHTMLToText(strings.Repeat("word ", commentfmt.MaxBodyBytes)))
	if !errors.Is(err, commentfmt.ErrTooLong) {
		t.Errorf("err = %v, want ErrTooLong", err)
	}
}

func TestCommentsResultCarriesNoPersonalData(t *testing.T) {
	d := parseCommentsFixture(t)
	cr := &CommentsResult{Imported: 3, PingsTotal: 2}
	for _, it := range d.Items {
		for _, c := range it.Comments {
			if !isPlainComment(c.Type) {
				cr.Pings = append(cr.Pings, it.Title+" (#"+c.ID+")")
			}
		}
	}
	b, err := jsonx.Marshal(cr)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range d.Items[0].Comments {
		for _, secret := range []string{c.AuthorEmail, c.IP} {
			if secret != "" && strings.Contains(string(b), secret) {
				t.Errorf("result JSON contains %q: %s", secret, b)
			}
		}
	}
	if strings.Contains(string(b), "@") || strings.Contains(string(b), "http") {
		t.Errorf("result JSON has an address or URL: %s", b)
	}
}
