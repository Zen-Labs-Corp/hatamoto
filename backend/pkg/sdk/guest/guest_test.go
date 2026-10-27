package guest

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestHandle(t *testing.T) {
	OnFilter(sdk.EntityBeforeSave, func(d sdk.EntityDraft) (sdk.EntityDraft, error) {
		if d.Name == "" {
			return d, Reject("name required")
		}
		d.Slug = strings.ToLower(d.Name)
		return d, nil
	})
	var seen string
	OnAction(sdk.EntityPublished, func(e sdk.EntityEvent) error { seen = e.ID; return nil })

	out, err := Handle("filter_entity_before_save", []byte(`{"name":"Hi"}`))
	if err != nil || !strings.Contains(string(out), `"slug":"hi"`) {
		t.Fatalf("filter: %s %v", out, err)
	}
	_, err = Handle("filter_entity_before_save", []byte(`{}`))
	if err == nil || err.Error() != sdk.RejectPrefix+"name required" {
		t.Fatalf("reject: %v", err)
	}
	if out, err := Handle("on_entity_published", []byte(`{"id":"x"}`)); err != nil || out != nil || seen != "x" {
		t.Fatalf("action: %v %v %q", out, err, seen)
	}
	if _, err := Handle("nope", nil); err == nil {
		t.Fatal("unknown export must error")
	}
}

func TestInvoke(t *testing.T) {
	var sent sdk.CallRequest
	fake := func(resp string) func([]byte) ([]byte, error) {
		return func(in []byte) ([]byte, error) {
			if err := json.Unmarshal(in, &sent); err != nil {
				t.Fatal(err)
			}
			return []byte(resp), nil
		}
	}
	var out struct{ Found bool }
	if err := Invoke(fake(`{"ok":true,"result":{"found":true}}`), sdk.FnKVGet, map[string]string{"key": "k"}, &out); err != nil || !out.Found {
		t.Fatalf("ok call: %v %+v", err, out)
	}
	if sent.Fn != sdk.FnKVGet || string(sent.Args) != `{"key":"k"}` {
		t.Fatalf("request = %+v", sent)
	}
	err := Invoke(fake(`{"ok":false,"error":{"code":"capability_denied","capability":"kv","message":"no"}}`), sdk.FnKVGet, nil, nil)
	var he *sdk.HostError
	if !errors.As(err, &he) || he.Code != sdk.ErrCapabilityDenied {
		t.Fatalf("typed error: %v", err)
	}
	if c, ok := CapabilityDenied(err); !ok || c != "kv" {
		t.Fatalf("CapabilityDenied = %q %v", c, ok)
	}
	if _, ok := CapabilityDenied(errors.New("x")); ok {
		t.Fatal("plain error reported as capability denied")
	}
	if err := Invoke(fake(`{"ok":false}`), "x", nil, nil); err == nil {
		t.Fatal("an error response without an error must still fail")
	}
}
