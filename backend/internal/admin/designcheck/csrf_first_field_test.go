package designcheck

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

var (
	multipartFormRe = regexp.MustCompile(`(?s)<form\b[^>]*enctype=["']multipart/form-data["'][^>]*>(.*?)</form>`)
	// firstFieldRe finds the first thing in a form body that is, or may emit, a
	// form control: the CSRF partial, another partial, or a control tag.
	firstFieldRe = regexp.MustCompile(`\{\{-?\s*template\s+"[^"]*"|<(?:input|select|textarea|button)\b[^>]*>`)
)

// misplacedCSRF returns the forms in src whose first field is not _csrf. The
// server reads the token of a multipart post from the first part of the body
// (checkToken), so a form that puts a file or any other input before it is
// refused with 403 for a browser without JavaScript.
func misplacedCSRF(src string) []string {
	var bad []string
	for _, m := range multipartFormRe.FindAllStringSubmatch(src, -1) {
		first := firstFieldRe.FindString(m[1])
		if !strings.Contains(first, `"hm/csrf"`) && !strings.Contains(first, `name="_csrf"`) {
			bad = append(bad, strings.TrimSpace(m[0][:min(len(m[0]), 120)]))
		}
	}
	return bad
}

// Every multipart upload form has _csrf as its first field (docs/ADMIN.md,
// Media).
func TestUILintMultipartFormsPutCSRFFirst(t *testing.T) {
	t.Parallel()

	fsys := os.DirFS(webAdmin)
	forms := 0
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		forms += len(multipartFormRe.FindAllString(string(b), -1))
		for _, f := range misplacedCSRF(string(b)) {
			t.Errorf("%s: multipart form without _csrf as its first field: %s", p, f)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if forms < 5 {
		t.Errorf("found only %d multipart forms: did the lint stop seeing the templates?", forms)
	}
}

func TestUILintMultipartCSRFSelfTest(t *testing.T) {
	t.Parallel()

	good := `<form method="post" enctype="multipart/form-data">{{template "hm/csrf" .}}<input type="file" name="f"></form>`
	badFile := `<form method="post" enctype="multipart/form-data"><input type="file" name="f">{{template "hm/csrf" .}}</form>`
	badPartial := `<form enctype="multipart/form-data">{{template "hm/form-upload-pkg" .}}{{template "hm/csrf" .}}</form>`
	goodSingle := `<form method='post' enctype='multipart/form-data'>{{template "hm/csrf" .}}<input type="file" name="f"></form>`
	goodTrim := `<form enctype="multipart/form-data">{{- template "hm/csrf" . -}}<input type="file" name="f"></form>`
	badSingle := `<form enctype='multipart/form-data'><input type="file" name="f">{{template "hm/csrf" .}}</form>`
	badTrim := `<form enctype="multipart/form-data">{{- template "hm/form-upload-pkg" . -}}{{template "hm/csrf" .}}</form>`
	plain := `<form method="post"><input name="a"></form>`
	if len(misplacedCSRF(goodSingle)) != 0 || len(misplacedCSRF(goodTrim)) != 0 {
		t.Error("a single-quoted or trimmed well-formed form was flagged")
	}
	if len(misplacedCSRF(badSingle)) != 1 || len(misplacedCSRF(badTrim)) != 1 {
		t.Error("a misplaced _csrf in a single-quoted or trimmed form was not flagged")
	}
	if len(misplacedCSRF(good)) != 0 || len(misplacedCSRF(plain)) != 0 {
		t.Error("a well-formed form was flagged")
	}
	if len(misplacedCSRF(badFile)) != 1 || len(misplacedCSRF(badPartial)) != 1 {
		t.Error("a misplaced _csrf was not flagged")
	}
}
