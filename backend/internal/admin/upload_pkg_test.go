package admin

import (
	"strings"
	"testing"
)

func TestPluginUploadPage(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	rec := g.do("GET", "/admin/plugins/upload", "owner-token", nil, nil)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{`type="file"`, `name="package"`, `action="/admin/plugins/upload"`, `enctype="multipart/form-data"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if rec := g.do("GET", "/admin/plugins/upload", "editor-token", nil, nil); rec.Code != 403 {
		t.Errorf("editor: %d", rec.Code)
	}
}

func TestPluginUploadHeaderOnePrimary(t *testing.T) {
	t.Parallel()

	g, _, _ := newPackageRig(t)
	for _, path := range []string{"/admin/plugins", "/admin/theme/themes"} {
		body := g.do("GET", path, "owner-token", nil, nil).Body.String()
		head := body[strings.Index(body, "<h1"):]
		head = head[:strings.Index(head, "</header>")+len("</header>")]
		if n := strings.Count(head, "hm-btn--primary"); n != 1 {
			t.Errorf("%s: %d primary buttons in the header, want 1", path, n)
		}
		if !strings.Contains(body, `data-hm-open="upload-pkg"`) || !strings.Contains(body, `<dialog class="hm-dialog" id="upload-pkg"`) {
			t.Errorf("%s: upload dialog trigger or dialog missing", path)
		}
		if strings.Contains(body, `id="upload"`) {
			t.Errorf("%s: the upload card is still there", path)
		}
	}
}
