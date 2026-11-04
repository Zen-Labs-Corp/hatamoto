package changeset

import "testing"

func TestDisplayChangeAltText(t *testing.T) {
	cases := []struct{ name, payload, wantOld, wantNew string }{
		{"empty old", `{"alt_text":"Five people"}`, "(empty)", "Five people"},
		{"old set", `{"old_alt":"a","alt_text":"b"}`, "a", "b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, n := displayChange(Item{Payload: []byte(c.payload)}, ReasonAltText, nil, "x.png")
			if o != c.wantOld || n != c.wantNew {
				t.Fatalf("got %q -> %q, want %q -> %q", o, n, c.wantOld, c.wantNew)
			}
		})
	}
}
