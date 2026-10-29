package menu

import (
	"errors"
	"strings"
	"testing"
)

func TestMenuItemAttrsValidate(t *testing.T) {
	long := strings.Repeat("a", 101)
	cases := []struct {
		name string
		a    MenuItemAttrs
		ok   bool
	}{
		{"empty", MenuItemAttrs{}, true},
		{"full", MenuItemAttrs{Style: "button", Color: "accent-2", Badge: "New", Icon: "arrow", ShowOn: "mobile", ID: "nav:a.b_c-1", Title: "t", AriaLabel: "a", Rel: []string{"nofollow", "ugc"}}, true},
		{"show all", MenuItemAttrs{ShowOn: "all"}, true},
		{"style upper", MenuItemAttrs{Style: "Button"}, false},
		{"style too long", MenuItemAttrs{Style: strings.Repeat("a", 33)}, false},
		{"color hex", MenuItemAttrs{Color: "#ff0000"}, false},
		{"icon space", MenuItemAttrs{Icon: "a b"}, false},
		{"badge long", MenuItemAttrs{Badge: long}, false},
		{"title control", MenuItemAttrs{Title: "a\nb"}, false},
		{"aria long", MenuItemAttrs{AriaLabel: long}, false},
		{"id digit first", MenuItemAttrs{ID: "1a"}, false},
		{"id quote", MenuItemAttrs{ID: `a"b`}, false},
		{"show on bad", MenuItemAttrs{ShowOn: "tablet"}, false},
		{"rel bad", MenuItemAttrs{Rel: []string{"noopener"}}, false},
	}
	for _, c := range cases {
		err := c.a.Validate()
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalidAttrs) {
			t.Errorf("%s: error does not wrap ErrInvalidAttrs: %v", c.name, err)
		}
	}
}

func TestMenuItemAttrsNormalizeShowAll(t *testing.T) {
	if got := (MenuItemAttrs{ShowOn: "all"}).Normalize().ShowOn; got != "" {
		t.Errorf("show_on = %q, want empty", got)
	}
}

func TestValidateCSSClasses(t *testing.T) {
	if got, err := ValidateCSSClasses("  footer-cta   _x -y "); err != nil || got != "footer-cta _x -y" {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := ValidateCSSClasses("  "); err != nil || got != "" {
		t.Errorf("blank: got %q, %v", got, err)
	}
	bad := []string{`a"b`, "1a", "a<b", "--x", "a{b}", strings.Repeat("a ", 11), strings.Repeat("a", 201), strings.Repeat("abcdefghi ", 10) + "x"}
	for _, in := range bad {
		if _, err := ValidateCSSClasses(in); !errors.Is(err, ErrInvalidAttrs) {
			t.Errorf("%q: err = %v, want ErrInvalidAttrs", in, err)
		}
	}
}

func TestValidLinkTypeNone(t *testing.T) {
	if !ValidLinkType(LinkTypeNone) || ValidLinkType("nope") {
		t.Error("none must be valid, unknown must not")
	}
}
