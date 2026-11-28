package wpimport

import (
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
)

const cf7Body = `<p>Intro</p>
[contact-form-7 id="123" title="Contact"]
[contact-form-7 id="a1b2c3d" title="Contact"]
[contact-form-7 123 "Contact"]
[contact-form-7 id="999" title="Other"]
<p>Outro</p>`

func TestConvertCF7WithoutOptionRemovesShortcode(t *testing.T) {
	c := Convert(cf7Body, true)
	if strings.Contains(c.HTML, "contact-form-7") || strings.Contains(c.HTML, "[form") {
		t.Errorf("html = %q", c.HTML)
	}
	if len(c.Shortcodes) != 1 || c.Shortcodes[0] != "contact-form-7" || len(c.Forms) != 1 || c.Forms[0] != "Contact Form 7" {
		t.Errorf("shortcodes=%v forms=%v", c.Shortcodes, c.Forms)
	}
}

func TestConvertCF7WithForms(t *testing.T) {
	c := Convert(cf7Body, true, WithForms(map[string]string{"123": "contact", "a1b2c3d": "contact"}))
	if n := strings.Count(c.HTML, `[form slug=&#34;contact&#34;]`); n != 3 {
		t.Errorf("form shortcodes = %d in %q", n, c.HTML)
	}
	if strings.Contains(c.HTML, "999") || strings.Contains(c.HTML, "contact-form-7") {
		t.Errorf("unmapped shortcode kept: %q", c.HTML)
	}
	// The unmapped form is still removed and reported.
	if len(c.Shortcodes) != 1 || c.Shortcodes[0] != "contact-form-7" {
		t.Errorf("shortcodes = %v", c.Shortcodes)
	}
	all := Convert(`[contact-form-7 id="123"]`, true, WithForms(map[string]string{"123": "contact"}))
	if len(all.Shortcodes) != 0 || len(all.Forms) != 0 {
		t.Errorf("replaced shortcode still reported: %v %v", all.Shortcodes, all.Forms)
	}
}

func TestConvertCF7FormSurvivesSanitiser(t *testing.T) {
	c := Convert(`[contact-form-7 id="123"]`, true, WithForms(map[string]string{"123": "contact"}))
	out := entity.SanitizeRichText(c.HTML)
	if !strings.Contains(out, "[form slug=") || !strings.Contains(out, "contact") {
		t.Errorf("sanitised = %q", out)
	}
}
