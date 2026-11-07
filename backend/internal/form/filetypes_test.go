package form

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func zipOf(t *testing.T, names ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte("<x/>"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCheckUpload(t *testing.T) {
	t.Parallel()
	pdf := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n")
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	jpg := append([]byte("\xff\xd8\xff\xe0\x00\x10JFIF"), make([]byte, 32)...)
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;")
	webp := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
	exe := append([]byte("MZ\x90\x00"), make([]byte, 64)...)
	html := []byte("<!DOCTYPE html><html><script>alert(1)</script></html>")
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>1</script></svg>`)
	tests := []struct {
		name string
		ext  string
		data []byte
		want string // content type, "" = ErrFileType
	}{
		{"pdf", "pdf", pdf, "application/pdf"},
		{"png", "png", png, "image/png"},
		{"jpg", "jpg", jpg, "image/jpeg"},
		{"jpeg", "jpeg", jpg, "image/jpeg"},
		{"gif", "gif", gif, "image/gif"},
		{"webp", "webp", webp, "image/webp"},
		{"txt", "txt", []byte("hello\nworld"), "text/plain"},
		{"csv", "csv", []byte("a,b\n1,2\n"), "text/csv"},
		{"json", "json", []byte(`{"a":1}`), "application/json"},
		{"md", "md", []byte("# title\n"), "text/markdown"},
		{"zip", "zip", zipOf(t, "a.txt"), "application/zip"},
		{"docx", "docx", zipOf(t, "[Content_Types].xml", "word/document.xml"), "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"xlsx", "xlsx", zipOf(t, "[Content_Types].xml"), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"pptx", "pptx", zipOf(t, "[Content_Types].xml"), "application/vnd.openxmlformats-officedocument.presentationml.presentation"},

		{"exe as pdf", "pdf", exe, ""},
		{"html as pdf", "pdf", html, ""},
		{"html as png", "png", html, ""},
		{"html as txt", "txt", html, ""},
		{"svg as txt", "txt", svg, ""},
		{"png as pdf", "pdf", png, ""},
		{"pdf as png", "png", pdf, ""},
		{"nul in text", "txt", append([]byte("hello"), 0, 'x'), ""},
		{"nul after sniff window", "txt", append(bytes.Repeat([]byte("a"), 2000), 0), ""},
		{"plain zip as docx", "docx", zipOf(t, "a.txt"), ""},
		{"text as docx", "docx", []byte("not a zip at all, just text"), ""},
		{"truncated zip as xlsx", "xlsx", zipOf(t, "[Content_Types].xml")[:30], ""},
		{"empty", "txt", nil, ""},
		{"svg ext not in table", "svg", svg, ""},
		{"html ext not in table", "html", html, ""},
		{"exe ext not in table", "exe", exe, ""},
		{"upper ext not in table", "PDF", pdf, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := CheckUpload(tc.ext, bytes.NewReader(tc.data), int64(len(tc.data)))
			if tc.want == "" {
				if !errors.Is(err, ErrFileType) {
					t.Fatalf("got (%q, %v), want ErrFileType", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got (%q, %v), want %q", got, err, tc.want)
			}
		})
	}
}

func TestAcceptTable(t *testing.T) {
	t.Parallel()
	exts := AcceptExts()
	if len(exts) != len(fileTypes) {
		t.Fatalf("AcceptExts has %d entries, table %d", len(exts), len(fileTypes))
	}
	for _, e := range exts {
		if !ValidAcceptExt(e) || e != strings.ToLower(e) {
			t.Errorf("bad ext %q", e)
		}
	}
	for _, banned := range []string{"svg", "html", "htm", "xml", "js", "exe", "xhtml", "php"} {
		if ValidAcceptExt(banned) {
			t.Errorf("%q must not be allowed", banned)
		}
	}
}
