package render

import (
	"strings"
	"testing"
)

func TestMarkDraftBanner(t *testing.T) {
	page := []byte(`<html><head><title>x</title></head><body class="a"><main>hi</main></body></html>`)

	got := string(markDraftBanner(page, &PageView{Preview: true, DraftBanner: true}))
	for _, want := range []string{`<p class="hm-preview-banner" role="note">`, "Preview of a draft.", "Not public.", "hm-preview-banner{"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<script") || strings.Contains(got, "data-hm-change") {
		t.Errorf("banner added a script or change marks:\n%s", got)
	}
	if i, j := strings.Index(got, "<body"), strings.Index(got, "hm-preview-banner\""); i > j {
		t.Error("banner is not the first thing in the body")
	}

	// Anything but a draft preview passes through unchanged.
	for name, v := range map[string]*PageView{
		"nil":            nil,
		"public":         {},
		"preview":        {Preview: true},
		"banner only":    {DraftBanner: true},
		"changeset mark": {Preview: true, Changes: &PageChanges{}},
	} {
		if out := markDraftBanner(page, v); string(out) != string(page) {
			t.Errorf("%s: output changed", name)
		}
	}
}
