package standing

import (
	"encoding/json"
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

const (
	manifestNoCreate   = `{"name":"p","runs":[{"name":"r","standing_approval":{"fields":["price"],"schemas":["product"]}}]}`
	manifestWithCreate = `{"name":"p","runs":[{"name":"r","standing_approval":{"fields":["price"],"schemas":["product"],"create":{"schemas":["product"],"max_per_hour":5}}}]}`
)

func TestCreatePolicyAbsentKeepsPolicyJSON(t *testing.T) {
	b, err := json.Marshal(Policy{Fields: []string{"price"}, Schemas: []string{"product"}, MaxPerHour: 10})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"fields":["price"],"schemas":["product"],"max_per_hour":10}`
	if string(b) != want {
		t.Fatalf("Policy JSON = %s, want %s", b, want)
	}
}

func TestCreatePolicyOldStoredJSONDecodesWithoutCreate(t *testing.T) {
	var p Policy
	old := []byte(`{"fields":["price"],"schemas":["product"],"max_per_hour":10}`)
	if err := jsonx.Unmarshal(old, &p, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	if p.Create != nil {
		t.Fatalf("Create = %+v, want nil", p.Create)
	}
	var withCreate Policy
	cur := []byte(`{"fields":["price"],"schemas":["product"],"max_per_hour":10,"create":{"schemas":["product"],"max_per_hour":5}}`)
	if err := jsonx.Unmarshal(cur, &withCreate, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	if c := withCreate.Create; c == nil || c.MaxPerHour != 5 || len(c.Schemas) != 1 || c.Schemas[0] != "product" {
		t.Fatalf("Create = %+v", c)
	}
}

func TestCreatePolicyChangesManifestHashAndVoidsApproval(t *testing.T) {
	without, err := ManifestHash([]byte(manifestNoCreate))
	if err != nil {
		t.Fatal(err)
	}
	with, err := ManifestHash([]byte(manifestWithCreate))
	if err != nil {
		t.Fatal(err)
	}
	if without == with {
		t.Fatal("adding create did not change the manifest hash")
	}
	a := Approval{
		Policy: Policy{Schemas: []string{"product"}},
		FP:     Fingerprint{Version: "1.0.0", ManifestHash: without, SchemaHashes: map[string]string{"product": "h"}},
	}
	cur := Fingerprint{Version: "1.0.0", ManifestHash: without, SchemaHashes: map[string]string{"product": "h"}}
	if !a.Matches(cur) {
		t.Fatal("unchanged plugin must still match")
	}
	cur.ManifestHash = with
	if a.Matches(cur) {
		t.Fatal("approval still matches after create was added")
	}
}

func TestCreatePolicyRuleWithoutCreateHashesAsBefore(t *testing.T) {
	got, err := ManifestHash([]byte(manifestNoCreate))
	if err != nil {
		t.Fatal(err)
	}
	const want = "523b1a84af282673b3daef5c4855a5be7a19cd482d1bcb18e2df9a05513db4ef"
	if got != want {
		t.Fatalf("ManifestHash = %q, want %q", got, want)
	}
}
