package updater

import "testing"

func TestAccessorsReturnInjectedValues(t *testing.T) {
	st := NewStore(nil)
	u, err := New(Env{Store: st, Version: "v1.4.1"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Store() != st {
		t.Fatal("Store() must return the injected store")
	}
	if u.Version() != "v1.4.1" {
		t.Fatalf("Version() = %q", u.Version())
	}
}
