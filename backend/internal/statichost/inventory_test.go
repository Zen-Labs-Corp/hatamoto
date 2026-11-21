package statichost

import (
	"regexp"
	"testing"
)

func TestInventoryShape(t *testing.T) {
	t.Parallel()
	routeRE := regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE) /`)
	keys := map[string]bool{}
	for _, it := range Inventory() {
		if keys[it.Key] {
			t.Errorf("duplicate key %q", it.Key)
		}
		keys[it.Key] = true
		if len(it.Routes) > 0 && it.Path != "" {
			t.Errorf("%s: has both Routes and Path", it.Key)
		}
		if len(it.Routes) == 0 && it.Path == "" {
			t.Errorf("%s: has neither Routes nor Path", it.Key)
		}
		if it.Where != Live && it.Where != Stays {
			t.Errorf("%s: bad Where %q", it.Key, it.Where)
		}
		for _, r := range it.Routes {
			if !routeRE.MatchString(r) {
				t.Errorf("%s: bad route %q", it.Key, r)
			}
		}
	}
	for _, k := range []string{"forms", "comments", "search", "consent", "track", "plugins"} {
		found := false
		for _, it := range Inventory() {
			if it.Key == k && it.Where == Live {
				found = true
			}
		}
		if !found {
			t.Errorf("live row %q missing", k)
		}
	}
}

func TestInventoryReturnsCopy(t *testing.T) {
	t.Parallel()
	a := Inventory()
	a[0].Key = "mutated"
	a[0].Routes[0] = "GET /mutated"
	b := Inventory()
	if b[0].Key == "mutated" || b[0].Routes[0] == "GET /mutated" {
		t.Error("Inventory must return a deep copy")
	}
}
