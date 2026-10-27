package wpimport

import "testing"

func TestHideAuthorsMovesOnlyHidden404s(t *testing.T) {
	a := &AuditResult{Failures: []AuditFailure{
		{URL: "/author/jane/", Status: 404, Reason: "answers 404"},
		{URL: "/author/bob/", Status: 404, Reason: "answers 404"},   // public person: a real failure
		{URL: "/author/admin/", Status: 500, Reason: "answers 500"}, // hidden person but not a 404: still a failure
		{URL: "/2024/01/gone/", Status: 404, Reason: "answers 404"},
	}}
	a.hideAuthors(map[string]string{"/author/jane": "Jane Doe", "/author/admin": "Admin"})
	if len(a.Hidden) != 1 || a.Hidden[0].URL != "/author/jane/" {
		t.Fatalf("hidden = %+v", a.Hidden)
	}
	if want := "author page hidden until Jane Doe is made public (People); then run the import again to redirect it"; a.Hidden[0].Reason != want {
		t.Errorf("reason = %q", a.Hidden[0].Reason)
	}
	if len(a.Failures) != 3 {
		t.Errorf("failures = %+v", a.Failures)
	}
	(*AuditResult)(nil).hideAuthors(map[string]string{"/x": "X"}) // nil-safe
}
