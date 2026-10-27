package admin

import (
	"os"
	"strings"
	"testing"
)

func readContentJS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../web/admin/js/content.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The picker body is injected with innerHTML, which fires no htmx:afterSettle, so openPicker
// must wire the dropzone itself or a dropped file navigates away from the editor.
func TestContentJSPickerInitialisesDropzone(t *testing.T) {
	t.Parallel()

	src := readContentJS(t)
	i := strings.Index(src, "async function openPicker")
	if i < 0 {
		t.Fatal("openPicker not found")
	}
	body := src[i:]
	if j := strings.Index(body, "\n}\n"); j > 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "initUpload(body)") {
		t.Error("openPicker must call initUpload(body) after injecting the picker")
	}
}

// htmx 2 gives htmx:xhr:loadend no xhr, so a status read from it is always undefined and the
// failure row never shows. The outcome has to come from htmx:afterRequest.
func TestContentJSUploadFailureUsesAfterRequest(t *testing.T) {
	t.Parallel()

	src := readContentJS(t)
	if strings.Contains(src, "detail?.xhr?.status") {
		t.Error("upload failure must not read a status from htmx:xhr:loadend")
	}
	if !strings.Contains(src, `"htmx:afterRequest"`) || !strings.Contains(src, "detail.successful") {
		t.Error("upload failure must listen to htmx:afterRequest and check detail.successful")
	}
}

// Remove rebuilds the file list, which detaches the focused button; the render must hand focus on.
// The choice itself is tested in scripts/admin-qa/unit/listlogic.test.mjs; this pins that the
// render uses it for the focus call (the other .focus() calls in the file do not count).
func TestContentJSRemoveKeepsFocus(t *testing.T) {
	t.Parallel()

	src := readContentJS(t)
	if !strings.Contains(src, "(buttons[removeFocusIndex(focusAt, buttons.length)] ?? input).focus()") {
		t.Error("removing a file must focus the Remove button chosen by removeFocusIndex, else the file input")
	}
	if !strings.Contains(src, "renumberRef(v, \"f\", i)") || !strings.Contains(src, "renumberOp(el.value, i)") {
		t.Error("renumberFormFields must use the tested renumberRef and renumberOp")
	}
}

// Each failed attempt replaces the previous "Try again" row instead of stacking another.
func TestContentJSUploadFailureReplacesThePreviousRow(t *testing.T) {
	t.Parallel()

	src := readContentJS(t)
	rm := strings.Index(src, `querySelectorAll("[data-hm-upload-failed]").forEach((old) => old.remove())`)
	add := strings.Index(src, "li.dataset.hmUploadFailed")
	if rm < 0 || add < 0 || rm > add {
		t.Error("the failure handler must remove earlier [data-hm-upload-failed] rows before adding its own")
	}
}
