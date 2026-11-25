package admin

import (
	"encoding/json/jsontext"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/core/internal/admin/schemaform"
	"hatamoto.dev/core/internal/jsonx"
)

// The boot lists the usable attributes of each schema for the query loop's sort and filter rows.
func TestBuilderBootSchemaAttrs(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.ents.addSchema(80, "Event", "event", `{"type":"object","properties":{"start_date":{"type":"string","format":"date","title":"Start date"},"body":{"type":"string","x-hm-field":"richtext"}}}`, `{}`, false)
	r.ents.addSchema(81, "Note", "note", `{"type":"object","properties":{"body":{"type":"string","x-hm-field":"richtext"}}}`, `{}`, false)
	r.ent.Attributes = jsontext.Value(builderOnlyDoc)
	rec := r.do("GET", r.path(), "owner-token", nil, nil)
	body := rec.Body.String()
	const open = `id="hm-builder-boot">`
	i := strings.Index(body, open)
	if rec.Code != http.StatusOK || i < 0 {
		t.Fatalf("builder screen: %d", rec.Code)
	}
	rest := body[i+len(open):]
	var boot struct {
		SchemaAttrs map[string][]schemaform.Attr `json:"schemaAttrs"`
	}
	if err := jsonx.Unmarshal([]byte(rest[:strings.Index(rest, "</script>")]), &boot, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	want := []schemaform.Attr{{Key: "start_date", Label: "Start date", Type: "date"}}
	if got := boot.SchemaAttrs["event"]; !reflect.DeepEqual(got, want) {
		t.Errorf("schemaAttrs[event] = %+v, want %+v", got, want)
	}
	if _, ok := boot.SchemaAttrs["note"]; ok {
		t.Error("a schema without usable attributes must not be sent")
	}
}
