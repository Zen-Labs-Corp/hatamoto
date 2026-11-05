package admin

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/a11y"
)

var pickerHeaders = map[string]string{"HX-Request": "true", "HX-Target": "hm-media-picker-body"}

func TestMediaPickerDocumentTypeListsOnlyPDFs(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	for i := 0; i < pickerPerPage; i++ { // enough PDFs for a second page
		m := c.media.add(300+i, "report-"+string(rune('a'+i))+".pdf", "application/pdf", "")
		m.Variants = nil // a stored PDF has no preview
	}

	body := c.do("GET", "/admin/media/picker?type=document", "owner-token", nil, pickerHeaders).Body.String()
	has(t, body, "report-a.pdf", `data-hm-pick="/media/x/file-300.png"`, "Search documents", `name="type" value="document"`,
		"application/pdf", "PDF, up to 10 MB each.", "type=document", "PDF")
	lacks(t, body, "team.png", "logo.png", "Search images", "image/png")
	if !strings.Contains(body, "/media/picker?page=2&amp;type=document") {
		t.Errorf("paging link lost the type:\n%s", body)
	}
	if issues := a11y.Check(body, a11y.Options{}); len(issues) > 0 {
		t.Errorf("document picker a11y: %v", issues)
	}

	body = c.do("GET", "/admin/media/picker?type=document&q=nothing-like-it", "owner-token", nil, pickerHeaders).Body.String()
	has(t, body, "No documents match")
}

func TestMediaPickerDefaultTypeIsImagesAndUntyped(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	for _, u := range []string{"/admin/media/picker", "/admin/media/picker?type=image", "/admin/media/picker?type=bogus"} {
		body := c.do("GET", u, "owner-token", nil, pickerHeaders).Body.String()
		has(t, body, "team.png", "Search images")
		lacks(t, body, "brochure.pdf", "type=document", `name="type"`)
	}
}

func TestMediaPickerUploadKeepsDocumentType(t *testing.T) {
	t.Parallel()
	c := newContentRig(t)
	body := c.do("GET", "/admin/media/picker?type=document", "owner-token", nil, pickerHeaders).Body.String()
	has(t, body, `hx-post="/admin/media/upload?picker=1&amp;type=document"`)
}
