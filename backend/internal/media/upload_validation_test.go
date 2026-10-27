package media

import "testing"

func TestImageTypeAndExtensionAllowed(t *testing.T) {
	for _, c := range []struct {
		ext, mime string
		want      bool
	}{
		{".webp", "image/jpeg", true},
		{".JPG", "image/webp", true},
		{".png", "image/gif", true},
		{".html", "image/png", false},
		{".svg", "image/png", false},
		{".png", "text/html; charset=utf-8", false},
		{".png", "image/svg+xml", false},
		{".pdf", "image/png", false},
		{".png", "application/pdf", false},
	} {
		if got := ImageTypeAndExtensionAllowed(c.ext, c.mime); got != c.want {
			t.Errorf("%s/%s = %v, want %v", c.ext, c.mime, got, c.want)
		}
	}
}

func TestVariantExtNamesWebPByRealFormat(t *testing.T) {
	if variantExt("image/webp", ".webp") != ".jpg" || variantExt("image/png", ".png") != ".png" {
		t.Error("variantExt wrong")
	}
}
