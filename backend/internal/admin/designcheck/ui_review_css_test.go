package designcheck

import (
	"strings"
	"testing"
)

func TestBlogEditorDialogLayout(t *testing.T) {
	t.Parallel()

	js := readWeb(t, "js/blogeditor.js")
	body := strings.Index(js, `class="hm-be__links-body"`)
	foot := strings.Index(js, `class="hm-dialog__foot"`)
	closeBody := strings.Index(js[body:], "</ul></div>")
	if body < 0 || foot < 0 || closeBody < 0 || body+closeBody > foot {
		t.Error("links dialog footer must follow .hm-be__links-body, not sit inside the scrolling list")
	}
	if !strings.Contains(readWeb(t, "pages/entities/blogedit.html"), `hm-row hm-be__dialog-tools`) ||
		!strings.Contains(readWeb(t, "css/pages/blogeditor.css"), ".hm-be__dialog-tools { margin-inline-start: auto; }") {
		t.Error("preview dialog tools must be right aligned")
	}
}

// A card form keeps the full card width of its siblings; only the body fields keep the 46rem measure.
func TestCardFormMeasure(t *testing.T) {
	t.Parallel()

	css := readWeb(t, "css/partials/card.css")
	if !strings.Contains(css, ".hm-cardform { display: block; }") || !strings.Contains(css, ".hm-cardform .hm-card__body > :not(table, .hm-setlist, .hm-table__scroll) { max-width: 46rem; }") {
		t.Error("card.css must leave .hm-cardform uncapped and cap only the body fields, not tables, .hm-table__scroll or .hm-setlist")
	}
}
