package render

import "testing"

func TestContactLink(t *testing.T) {
	d := contactDefaults{Phone: "+41 79 123 45 67", Email: "shop@example.org"}
	cases := []struct {
		name, channel, to, msg string
		want                   string
	}{
		{"whatsapp default", "whatsapp", "", "", "https://wa.me/41791234567"},
		{"whatsapp text", "whatsapp", "(079) 123-45-67", "Hi & bye é", "https://wa.me/0791234567?text=Hi%20%26%20bye%20%C3%A9"},
		{"call keeps plus", "call", "+41 79 123 45 67", "", "tel:+41791234567"},
		{"call default", "call", "", "", "tel:+41791234567"},
		{"sms body", "sms", "0791234567", "Hello there", "sms:0791234567?body=Hello%20there"},
		{"email default", "email", "", "", "mailto:shop@example.org"},
		{"email body", "email", "a@example.com", "x y", "mailto:a@example.com?body=x%20y"},
		{"too short", "whatsapp", "123", "", ""},
		{"too long", "call", "1234567890123456", "", ""},
		{"header injection", "email", "a@example.com?bcc=x@y.z", "", ""},
		{"newline", "email", "a@example.com\nbcc:x@y.z", "", ""},
		{"list", "email", "a@example.com,b@example.com", "", ""},
		{"unknown channel", "fax", "0791234567", "", ""},
	}
	for _, c := range cases {
		got, ok := contactLink(c.channel, c.to, c.msg, d)
		if (c.want == "") == ok || string(got) != c.want {
			t.Errorf("%s: got %q ok=%v, want %q", c.name, got, ok, c.want)
		}
	}
	if _, ok := contactLink("whatsapp", "", "", contactDefaults{}); ok {
		t.Error("no number anywhere must not link")
	}
	if _, ok := contactLink("email", "", "", contactDefaults{Phone: "0791234567"}); ok {
		t.Error("no address anywhere must not link")
	}
}

func TestContactLabel(t *testing.T) {
	if contactLabel("call", "  Ring us ") != "Ring us" || contactLabel("call", "") != "Call us" ||
		contactLabel("whatsapp", "") != "Chat on WhatsApp" {
		t.Error("label fallback")
	}
}

func TestContactDefaultsFallBackToSite(t *testing.T) {
	v := minimalView()
	v.Site.Phone, v.Site.Org.Email = "0791111111", "org@example.org"
	if d := contactDefaultsOf(v); d.Phone != "0791111111" || d.Email != "org@example.org" {
		t.Errorf("site fallback: %+v", d)
	}
	v.Contact = ContactView{Phone: "0792222222", Email: "fr@example.org"}
	if d := contactDefaultsOf(v); d.Phone != "0792222222" || d.Email != "fr@example.org" {
		t.Errorf("locale contact: %+v", d)
	}
}
