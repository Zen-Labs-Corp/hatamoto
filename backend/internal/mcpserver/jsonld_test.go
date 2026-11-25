package mcpserver

import (
	"encoding/json/jsontext"
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/entity"
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

const eventBlock = `{"@context":"https://schema.org","@type":"Event","name":"N","startDate":"2026-12-05","location":"Hall"}`

func TestAddAnnotationFindings(t *testing.T) {
	valid := ValidateJSONLD([][]byte{[]byte(eventBlock)})
	if !valid.Valid {
		t.Fatalf("event block invalid: %+v", valid)
	}

	rep := AddAnnotationFindings(valid, "Event", []string{"bad mapping"}, []string{"soft"})
	if rep.Valid {
		t.Error("error left the report valid")
	}
	if len(rep.Nodes) != 1 || rep.Nodes[0].Errors[len(rep.Nodes[0].Errors)-1] != "bad mapping" || rep.Nodes[0].Warnings[len(rep.Nodes[0].Warnings)-1] != "soft" {
		t.Errorf("findings not on the Event node: %+v", rep.Nodes)
	}

	warnOnly := AddAnnotationFindings(ValidateJSONLD([][]byte{[]byte(eventBlock)}), "Event", nil, []string{"soft"})
	if !warnOnly.Valid {
		t.Error("warning made the report invalid")
	}

	article := ValidateJSONLD([][]byte{[]byte(`{"@context":"https://schema.org","@type":"Article","headline":"H"}`)})
	n := len(article.Nodes)
	rep = AddAnnotationFindings(article, "Event", []string{"bad mapping"}, nil)
	if len(rep.Nodes) != n+1 || rep.Nodes[n].Type != "Event" || rep.Nodes[n].Errors[0] != "bad mapping" {
		t.Errorf("no new Event node: %+v", rep.Nodes)
	}

	same := ValidateJSONLD([][]byte{[]byte(eventBlock)})
	if got := AddAnnotationFindings(same, "Event", nil, nil); !reflect.DeepEqual(got, ValidateJSONLD([][]byte{[]byte(eventBlock)})) {
		t.Errorf("no findings changed the report: %+v", got)
	}
}

func TestJSONLDValidateSchemaMapping(t *testing.T) {
	schema := `{"type":"object","x-schema-org-type":"Event","properties":{"status":{"type":"string","enum":["cancelled"],"x-schema-org-property":"eventStatus","x-schema-org-enum":{"canceled":"https://schema.org/EventCancelled","cancelled":"EventGone/.."}}}}`
	f := entity.CheckSchemaAnnotations(jsontext.Value(schema))
	rep := AddAnnotationFindings(ValidateJSONLD([][]byte{[]byte(eventBlock)}), f.RootType, f.Errors, f.Warnings)
	if rep.Valid {
		t.Fatal("report valid")
	}
	joined := strings.Join(rep.Nodes[0].Errors, "|")
	for _, want := range []string{
		`x-schema-org-enum on "status": "canceled" is not one of the enum values`,
		`x-schema-org-enum on "status": "EventGone/.." is not a schema.org URL or term`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
}
