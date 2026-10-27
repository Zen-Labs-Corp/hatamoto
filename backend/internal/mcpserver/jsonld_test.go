package mcpserver

import (
	"strings"
	"testing"
)

func TestValidateJSONLD(t *testing.T) {
	good := `{"@context":"https://schema.org","@type":"BlogPosting","headline":"H","author":{"@type":"Person","name":"A"},"datePublished":"2026-01-01","image":"x","dateModified":"2026-01-02","publisher":{"@type":"Organization","name":"O","url":"u","logo":"l"}}`
	rep := ValidateJSONLD([][]byte{[]byte(good)})
	if !rep.Valid {
		t.Fatalf("good block invalid: %+v", rep)
	}

	bad := `{"@context":"https://schema.org","@graph":[{"@type":"Article","headline":"H"},{"@type":"FAQPage","mainEntity":[{"@type":"Question","name":"q"}]}]}`
	rep = ValidateJSONLD([][]byte{[]byte(bad)})
	if rep.Valid {
		t.Fatalf("bad block valid: %+v", rep)
	}
	var all []string
	for _, n := range rep.Nodes {
		all = append(all, n.Errors...)
	}
	joined := strings.Join(all, "|")
	for _, want := range []string{`Article: missing required field "author"`, `Article: missing required field "datePublished"`, `Question: missing required field "acceptedAnswer"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing finding %q in %s", want, joined)
		}
	}

	for name, in := range map[string]string{
		"not json":   `{`,
		"no context": `{"@type":"Person","name":"x"}`,
		"no type":    `{"@context":"https://schema.org","name":"x"}`,
	} {
		if r := ValidateJSONLD([][]byte{[]byte(in)}); r.Valid {
			t.Errorf("%s: valid", name)
		}
	}
	if r := ValidateJSONLD(nil); r.Valid {
		t.Error("no blocks: valid")
	}
}

func TestDateRange(t *testing.T) {
	if _, err := dateRange("2026-02-01", "2026-01-01"); err == nil {
		t.Error("from after to accepted")
	}
	if _, err := dateRange("2020-01-01", "2026-01-01"); err == nil {
		t.Error("huge range accepted")
	}
	if _, err := dateRange("nope", ""); err == nil {
		t.Error("bad date accepted")
	}
	if rg, err := dateRange("", ""); err != nil || rg.To.Sub(rg.From).Hours() != 29*24 {
		t.Errorf("default range = %v %v", rg, err)
	}
}

func TestAuditArgsShortensAndCaps(t *testing.T) {
	long := strings.Repeat("a", 5000)
	got := string(auditArgs(map[string]any{"data_base64": long, "n": 1}))
	if len(got) > maxAuditArgs || !strings.Contains(got, "5000 bytes") {
		t.Errorf("args not shortened: %.120s", got)
	}
}

// An organisation-credited article names its author by @id only.
func TestValidateJSONLDAcceptsAnIDOnlyAuthor(t *testing.T) {
	doc := `{"@context":"https://schema.org","@type":"Article","headline":"H","author":{"@id":"https://x.test#organization"},"datePublished":"2026-01-01"}`
	if rep := ValidateJSONLD([][]byte{[]byte(doc)}); !rep.Valid {
		t.Errorf("id-only author rejected: %+v", rep)
	}
}
