package comment

import (
	"errors"
	"testing"
	"time"

	"hatamoto.dev/core/internal/jsonx"
)

func TestDecodePolicy(t *testing.T) {
	t.Parallel()
	def := DefaultPolicy()
	cases := []struct {
		name string
		raw  string
		want Policy
	}{
		{"empty", "", def},
		{"null", "null", def},
		{"junk", "{nope", def},
		{"string", `"on"`, def},
		{"full", `{"enabled":true,"max_depth":32,"edit_window_minutes":120,"slow_mode_seconds":30}`, Policy{true, 32, 120, 30}},
		{"enabled only", `{"enabled":true}`, Policy{Enabled: true, MaxDepth: 10, EditWindowMinutes: 15}},
		{"enabled string", `{"enabled":"true","max_depth":5}`, Policy{MaxDepth: 5, EditWindowMinutes: 15}},
		{"depth too big", `{"enabled":true,"max_depth":33}`, Policy{Enabled: true, MaxDepth: 10, EditWindowMinutes: 15}},
		{"depth zero", `{"enabled":true,"max_depth":0}`, Policy{Enabled: true, MaxDepth: 10, EditWindowMinutes: 15}},
		{"depth fraction", `{"enabled":true,"max_depth":2.5}`, Policy{Enabled: true, MaxDepth: 10, EditWindowMinutes: 15}},
		{"depth string", `{"enabled":true,"max_depth":"4"}`, Policy{Enabled: true, MaxDepth: 10, EditWindowMinutes: 15}},
		{"window too big", `{"enabled":true,"edit_window_minutes":1441}`, Policy{Enabled: true, MaxDepth: 10, EditWindowMinutes: 15}},
		{"slow negative", `{"enabled":true,"slow_mode_seconds":-1}`, Policy{Enabled: true, MaxDepth: 10, EditWindowMinutes: 15}},
		{"slow too big", `{"enabled":true,"slow_mode_seconds":86401}`, Policy{Enabled: true, MaxDepth: 10, EditWindowMinutes: 15}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := DecodePolicy([]byte(c.raw)); got != c.want {
				t.Errorf("DecodePolicy(%q) = %+v, want %+v", c.raw, got, c.want)
			}
		})
	}
}

func TestPolicyValidate(t *testing.T) {
	t.Parallel()
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatalf("default policy: %v", err)
	}
	if err := (Policy{MaxDepth: 32, EditWindowMinutes: 1440, SlowModeSeconds: 86400}).Validate(); err != nil {
		t.Fatalf("upper bounds: %v", err)
	}
	cases := []struct {
		p     Policy
		field string
	}{
		{Policy{MaxDepth: 0, EditWindowMinutes: 15}, "policy.max_depth"},
		{Policy{MaxDepth: 33, EditWindowMinutes: 15}, "policy.max_depth"},
		{Policy{MaxDepth: 10, EditWindowMinutes: 0}, "policy.edit_window_minutes"},
		{Policy{MaxDepth: 10, EditWindowMinutes: 1441}, "policy.edit_window_minutes"},
		{Policy{MaxDepth: 10, EditWindowMinutes: 15, SlowModeSeconds: -1}, "policy.slow_mode_seconds"},
		{Policy{MaxDepth: 10, EditWindowMinutes: 15, SlowModeSeconds: 86401}, "policy.slow_mode_seconds"},
	}
	for _, c := range cases {
		var fe *FieldError
		if err := c.p.Validate(); !errors.As(err, &fe) || fe.Field != c.field {
			t.Errorf("Validate(%+v) = %v, want field %s", c.p, err, c.field)
		}
	}
}

func TestPolicyAccessors(t *testing.T) {
	t.Parallel()
	off := Policy{MaxDepth: 32, EditWindowMinutes: 120, SlowModeSeconds: 30}
	if off.Depth(4) != 4 || off.EditWindow() != EditWindow || off.SlowMode(7) != 7*time.Second {
		t.Errorf("off policy must return the site values: %d %v %v", off.Depth(4), off.EditWindow(), off.SlowMode(7))
	}
	on := Policy{Enabled: true, MaxDepth: 32, EditWindowMinutes: 120}
	if on.Depth(4) != 32 || on.EditWindow() != 2*time.Hour || on.SlowMode(7) != 0 {
		t.Errorf("on policy must use its own values: %d %v %v", on.Depth(4), on.EditWindow(), on.SlowMode(7))
	}
	on.SlowModeSeconds = 30
	if on.SlowMode(7) != 30*time.Second {
		t.Errorf("SlowMode = %v, want 30s", on.SlowMode(7))
	}
}

func TestValidOrder(t *testing.T) {
	t.Parallel()
	for _, o := range []string{"", "newest", "oldest", "rank"} {
		if !ValidOrder(o) {
			t.Errorf("ValidOrder(%q) = false", o)
		}
	}
	if ValidOrder("random") {
		t.Error(`ValidOrder("random") = true`)
	}
}

func TestMergeSchemaComments(t *testing.T) {
	t.Parallel()
	pol := Policy{Enabled: true, MaxDepth: 20, EditWindowMinutes: 60, SlowModeSeconds: 5}
	order := "oldest"
	raw := []byte(`{"url_pattern":"/{slug}","comments":{"mode":"closed","x":1}}`)

	out, err := MergeSchemaComments(raw, &order, &pol)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := jsonx.Unmarshal(out, &m, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	cm, _ := m["comments"].(map[string]any)
	if m["url_pattern"] != "/{slug}" || cm["mode"] != "closed" || cm["x"] != float64(1) || cm["order"] != "oldest" {
		t.Fatalf("keys lost or order missing: %s", out)
	}
	if got := DecodePolicy(mustRaw(t, cm["policy"])); got != pol {
		t.Errorf("policy round trip = %+v, want %+v", got, pol)
	}

	// order nil keeps the order; policy nil keeps the policy.
	again, err := MergeSchemaComments(out, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if DecodePolicy(mustRaw(t, policyOf(t, again))) != pol {
		t.Errorf("nil,nil must change nothing: %s", again)
	}

	// "" deletes the order only.
	empty := ""
	cleared, err := MergeSchemaComments(out, &empty, nil)
	if err != nil {
		t.Fatal(err)
	}
	m = nil
	if err := jsonx.Unmarshal(cleared, &m, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	cm, _ = m["comments"].(map[string]any)
	if _, has := cm["order"]; has || cm["mode"] != "closed" || cm["policy"] == nil {
		t.Errorf("clearing order must keep the other keys: %s", cleared)
	}

	// Empty and null documents.
	for _, in := range []string{"", "null", "{}"} {
		got, err := MergeSchemaComments([]byte(in), nil, &pol)
		if err != nil || DecodePolicy(mustRaw(t, policyOf(t, got))) != pol {
			t.Errorf("merge into %q = %s, %v", in, got, err)
		}
	}
	if _, err := MergeSchemaComments([]byte("{nope"), nil, &pol); err == nil {
		t.Error("junk settings must error")
	}
}

func policyOf(t *testing.T, raw []byte) any {
	t.Helper()
	var m map[string]any
	if err := jsonx.Unmarshal(raw, &m, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	cm, _ := m["comments"].(map[string]any)
	return cm["policy"]
}

func mustRaw(t *testing.T, v any) []byte {
	t.Helper()
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
