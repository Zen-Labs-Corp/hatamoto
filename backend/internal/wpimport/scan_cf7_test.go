package wpimport

import (
	"strings"
	"testing"
)

func scanCF7Dump(forms []CF7Form) *Dump {
	return &Dump{
		Detect: Detection{Namespaces: []string{"contact-form-7/v1"}},
		CF7:    forms,
		Items: []Item{
			{ID: "1", Type: "page", Status: "publish", Title: "Mapped", Content: `<p>Hi</p><p>[contact-form-7 id="123" title="Contact"]</p>`},
			{ID: "2", Type: "page", Status: "publish", Title: "Unmapped", Content: `<p>Hi</p><p>[contact-form-7 id="999" title="Other"]</p>`},
		},
	}
}

func TestScanCF7ImportedFormIsNotLossy(t *testing.T) {
	r := Scan(scanCF7Dump([]CF7Form{{ID: "123", Hash: "a1b2c3d", Slug: "contact", Title: "Contact"}}), "test")
	if r.FormsImported != 1 {
		t.Errorf("FormsImported = %d, want 1", r.FormsImported)
	}
	if r.Fidelity.High != 1 || r.Fidelity.Medium != 1 {
		t.Errorf("Fidelity = %+v, want the mapped page high and the unmapped one medium", r.Fidelity)
	}
	if len(r.LowFidelity) != 1 || r.LowFidelity[0].ID != "2" {
		t.Errorf("LowFidelity = %+v, want only the unmapped page", r.LowFidelity)
	}
	if r.Shortcodes["contact-form-7"] != 1 {
		t.Errorf("Shortcodes = %v, want one removed (the unmapped one)", r.Shortcodes)
	}
	if got := strings.Join(r.Forms, ","); got != "Contact Form 7" {
		t.Errorf("Forms = %q, the unmapped shortcode keeps the plugin listed", got)
	}
	if s := r.Summary(); !strings.Contains(s, "Forms imported: 1") {
		t.Errorf("summary lacks the imported line:\n%s", s)
	}
}

func TestScanCF7AllMappedReportsNothingRemoved(t *testing.T) {
	d := scanCF7Dump([]CF7Form{{ID: "123", Slug: "contact"}, {ID: "999", Slug: "other"}})
	r := Scan(d, "test")
	if r.FormsImported != 2 || len(r.Forms) != 0 || len(r.Shortcodes) != 0 || len(r.LowFidelity) != 0 || r.Fidelity.High != 2 {
		t.Errorf("report = %+v", r)
	}
	if s := r.Summary(); strings.Contains(s, "recreate") || strings.Contains(s, "removed") {
		t.Errorf("summary still says removed:\n%s", s)
	}
}

func TestScanCF7WithoutItemsKeepsWording(t *testing.T) {
	r := Scan(scanCF7Dump(nil), "test")
	if r.FormsImported != 0 || r.Fidelity.Medium != 2 || len(r.Forms) != 1 {
		t.Errorf("report = %+v", r)
	}
	if !strings.Contains(r.LowFidelity[0].Reason, "recreate it with Hatamoto forms") {
		t.Errorf("reason = %q", r.LowFidelity[0].Reason)
	}
}
