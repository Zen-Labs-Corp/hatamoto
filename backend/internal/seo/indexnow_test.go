package seo

import (
	"regexp"
	"testing"
)

func TestIndexNowHostAndIsZero(t *testing.T) {
	var n IndexNow
	if !n.IsZero() || n.Host() != "api.indexnow.org" {
		t.Fatalf("zero: %v %q", n.IsZero(), n.Host())
	}
	n = IndexNow{Endpoint: "www.bing.com"}
	if n.IsZero() || n.Host() != "www.bing.com" {
		t.Fatalf("set: %v %q", n.IsZero(), n.Host())
	}
}

func TestNewIndexNowKey(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{32}$`)
	a, b := NewIndexNowKey(), NewIndexNowKey()
	if !re.MatchString(a) || a == b {
		t.Fatalf("keys %q %q", a, b)
	}
	if errs := validateIndexNow(IndexNow{Key: a}); len(errs) != 0 {
		t.Fatal(errs)
	}
}

func TestValidateIndexNow(t *testing.T) {
	cases := []struct {
		name string
		n    IndexNow
		want string
	}{
		{"ok", IndexNow{Enabled: true, Key: "abcdef12", Endpoint: "yandex.com"}, ""},
		{"endpoint", IndexNow{Endpoint: "evil.example"}, "indexnow[endpoint]"},
		{"short key", IndexNow{Key: "abc"}, "indexnow[key]"},
		{"bad key", IndexNow{Key: "abcdefgh/../x"}, "indexnow[key]"},
	}
	for _, c := range cases {
		errs := validateIndexNow(c.n)
		if c.want == "" {
			if len(errs) != 0 {
				t.Errorf("%s: %v", c.name, errs)
			}
			continue
		}
		if len(errs) != 1 || errs[0].Field != c.want {
			t.Errorf("%s: %v", c.name, errs)
		}
	}
}
