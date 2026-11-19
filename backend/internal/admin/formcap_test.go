package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// bigFormBody is a multipart body over net/http's 32 MiB in-memory threshold.
func bigFormBody(t *testing.T) ([]byte, string) {
	t.Helper()
	buf, ct := multipartBody(t, map[string][]byte{"big.bin": make([]byte, 33<<20)}, true)
	return buf.Bytes(), ct
}

//nolint:paralleltest // sets TMPDIR
func TestFormBodyCapLeavesNoTempFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	c := newContentRig(t)
	raw, ct := bigFormBody(t)

	for name, tc := range map[string]struct{ target, session string }{
		"pre-session": {"/admin/login", ""},
		"signed in":   {"/admin/entities/" + fid(12).String() + "/status", "editor-token"},
	} {
		req := httptest.NewRequest("POST", tc.target, bytes.NewReader(raw))
		req.Header.Set("Content-Type", ct)
		if tc.session != "" {
			req.AddCookie(&http.Cookie{Name: "hatamoto_session", Value: tc.session})
		}
		c.addFetchMeta(req, tc.session, nil)
		rec := httptest.NewRecorder()
		c.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: status %d, want 413", name, rec.Code)
		}
	}
	left, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("temp files left behind: %d", len(left))
	}
}

//nolint:paralleltest // sets TMPDIR
func TestFormBodyCapKeepsUploadRoutes(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	c := newContentRig(t)
	rec := c.upload(t, "/admin/media/upload", map[string][]byte{"big.png": append(append([]byte{}, png1x1...), make([]byte, 9<<20)...)}, true)
	if rec.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("media upload hit the form cap: %d", rec.Code)
	}
}

func TestOwnBodyLimitAllowlist(t *testing.T) {
	t.Parallel()
	a := newRig(t).a
	id := fid(51).String()
	for p, want := range map[string]bool{
		"/admin/media/upload":               true,
		"/admin/media/" + id + "/replace":   true,
		"/admin/plugins/upload":             true,
		"/admin" + csvBase:                  true,
		"/admin" + importBase + "/x/start":  true,
		"/admin/media/" + id + "/delete":    false,
		"/admin/entities/" + id + "/status": false,
		"/admin/login":                      false,
		"/admin/plugins/upload-not":         false,
	} {
		if got := a.ownBodyLimit(p); got != want {
			t.Errorf("ownBodyLimit(%q) = %v, want %v", p, got, want)
		}
	}
}
