package siteconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContactButtonMergeKeepsOtherKeys(t *testing.T) {
	in := []byte(`{"builder":{"tokens":{"a":"b"}},"socialLinks":[{"u":"x"}]}`)
	want := ContactButton{Enabled: true, Channel: "whatsapp", Position: "bottom-left", Label: "Chat", To: "+41 79 123 45 67", Message: "Hi"}
	out, err := MergeContactButton(in, want)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatal(err)
	}
	if string(top["builder"]) != `{"tokens":{"a":"b"}}` || string(top["socialLinks"]) != `[{"u":"x"}]` {
		t.Fatalf("other keys changed: %s", out)
	}
	if got := ParseContactButton(out); got != want {
		t.Fatalf("round trip: %+v", got)
	}
	if _, err := MergeContactButton(nil, want); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeContactButton([]byte(`[1]`), want); err == nil {
		t.Fatal("array theme settings must be refused")
	}
}

func TestContactButtonParseBadData(t *testing.T) {
	for _, in := range []string{``, `null`, `{}`, `[`, `{"contactButton":"x"}`} {
		if got := ParseContactButton([]byte(in)); got != (ContactButton{}) {
			t.Errorf("%q: %+v", in, got)
		}
	}
}

func TestContactButtonValidate(t *testing.T) {
	ok := ContactButton{Channel: "call", Position: "bottom-right"}
	if e := ok.Validate(); len(e) != 0 {
		t.Fatalf("valid: %v", e)
	}
	cases := map[string]func(c *ContactButton){
		"channel":  func(c *ContactButton) { c.Channel = "fax" },
		"position": func(c *ContactButton) { c.Position = "top" },
		"label":    func(c *ContactButton) { c.Label = strings.Repeat("é", 61) },
		"message":  func(c *ContactButton) { c.Message = strings.Repeat("a", 501) },
		"to":       func(c *ContactButton) { c.To = "12345" },
	}
	for field, mut := range cases {
		c := ok
		mut(&c)
		e := c.Validate()
		if len(e) != 1 || e[0].Field != field {
			t.Errorf("%s: %v", field, e)
		}
	}
	good := []ContactButton{
		{Channel: "whatsapp", Position: "bottom-left", To: "+41 (79) 123-45-67"},
		{Channel: "email", Position: "bottom-left", To: "a@example.com"},
		{Channel: "sms", Position: "bottom-left", To: "0791234567"},
		{Channel: "whatsapp", Position: "bottom-left", To: "0041 79 123 45 67"},
		{Channel: "whatsapp", Position: "bottom-left", To: "+41 (0)79 123 45 67"},
		{Channel: "call", Position: "bottom-left", Label: strings.Repeat("é", 60)},
	}
	for _, c := range good {
		if e := c.Validate(); len(e) != 0 {
			t.Errorf("%+v: %v", c, e)
		}
	}
	bad := []ContactButton{
		{Channel: "whatsapp", Position: "bottom-left", To: "1234567890123456"},
		{Channel: "call", Position: "bottom-left", To: "079abc1234"},
		{Channel: "whatsapp", Position: "bottom-left", To: "079 123 45 67"},
		{Channel: "email", Position: "bottom-left", To: "a@example.com?bcc=x@y.z"},
		{Channel: "email", Position: "bottom-left", To: "a@example.com,b@example.com"},
		{Channel: "email", Position: "bottom-left", To: "a@example.com\nbcc: x"},
		{Channel: "email", Position: "bottom-left", To: "Bob <a@example.com>"},
		{Channel: "email", Position: "bottom-left", To: "nope"},
	}
	for _, c := range bad {
		if e := c.Validate(); len(e) != 1 || e[0].Field != "to" {
			t.Errorf("%+v: %v", c, e)
		}
	}
}
