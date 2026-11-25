package schemaform

import (
	"strings"
	"testing"
)

func TestRenderSecretStates(t *testing.T) {
	t.Parallel()

	const lost = "The stored value cannot be read (the server's secret changed). Enter it again to use this plugin."
	plain := []byte(`{"type":"object","properties":{"api_key":{"type":"string","title":"API key","writeOnly":true}}}`)
	described := []byte(`{"type":"object","properties":{"api_key":{"type":"string","title":"API key","writeOnly":true,"description":"From your dashboard."}}}`)

	cases := []struct {
		name       string
		schema     []byte
		opt        Options
		want, deny []string
	}{
		{"not stored", plain, Options{},
			[]string{`placeholder="Enter API key"`},
			[]string{"-clear", "-lost", "Stored. Leave blank", "Enter this value again"}},
		{"stored", plain, Options{Secrets: map[string]bool{"api_key": true}},
			[]string{`placeholder="Stored. Leave blank to keep it."`, `id="sf-api_key-clear"`},
			[]string{"-lost", "Enter this value again"}},
		{"unreadable", plain, Options{Unreadable: map[string]bool{"api_key": true}},
			[]string{`placeholder="Enter this value again"`, lost, `id="sf-api_key-lost"`, `id="sf-api_key-clear"`, `aria-describedby="sf-api_key-lost"`},
			[]string{"Stored. Leave blank"}},
		{"unreadable with description", described, Options{Unreadable: map[string]bool{"api_key": true}},
			[]string{`aria-describedby="sf-api_key-lost sf-api_key-help"`, "From your dashboard.", lost, `id="sf-api_key-clear"`},
			[]string{"Stored. Leave blank"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := Render(tc.schema, nil, tc.opt)
			if err != nil {
				t.Fatal(err)
			}
			got := string(out)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, d := range tc.deny {
				if strings.Contains(got, d) {
					t.Errorf("unexpected %q in:\n%s", d, got)
				}
			}
		})
	}
}
