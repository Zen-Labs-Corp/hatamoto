package schemafield

import "testing"

func TestIsURIField(t *testing.T) {
	for _, c := range []struct {
		format, xHm string
		want        bool
	}{
		{"uri", "", true}, {"", "url", true}, {"", "image", true}, {"", "", false},
		{"uri", "richtext", false}, {"uri", "code", false}, {"text", "", false},
	} {
		if got := IsURIField(c.format, c.xHm); got != c.want {
			t.Errorf("IsURIField(%q, %q) = %v, want %v", c.format, c.xHm, got, c.want)
		}
	}
}

func TestMentionsMediaPath(t *testing.T) {
	for in, want := range map[string]bool{
		"/media/a.jpg": true, "media/a.jpg": true, "https://example.com/Media/a.jpg": true,
		`<img src="/uploads/x.png">`: true, "a%2Fmedia%2Fb": true,
		"": false, "Hello": false, "/mediation/x": false, "https://example.com/about": false,
	} {
		if got := MentionsMediaPath(in); got != want {
			t.Errorf("MentionsMediaPath(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsImageAttributeName(t *testing.T) {
	for _, k := range []string{"og_image", "featured_image", "image", "photo", "OG_Image"} {
		if !IsImageAttributeName(k) {
			t.Errorf("%q is read as an image by name", k)
		}
	}
	if IsImageAttributeName("title") {
		t.Error("title is not an image")
	}
}

func TestEffectiveFormat(t *testing.T) {
	for _, c := range []struct{ format, hint, want string }{
		{"", "url", "uri"}, {"", "image", "uri"}, {"uri", "richtext", "richtext"},
		{"uri", "code", "textarea"}, {"uri", "html", "textarea"}, {"", "css", "textarea"},
		{"uri", "", "uri"}, {"date", "other", "date"}, {"", "", ""},
	} {
		if got := EffectiveFormat(c.format, c.hint); got != c.want {
			t.Errorf("EffectiveFormat(%q,%q) = %q, want %q", c.format, c.hint, got, c.want)
		}
	}
}
