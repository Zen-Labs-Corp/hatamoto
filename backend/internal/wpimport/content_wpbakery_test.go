package wpimport

import (
	"reflect"
	"testing"
)

func TestVCAttrs(t *testing.T) {
	cases := map[string]struct {
		in   string
		want map[string]string
	}{
		"straight":   {` text="Hello" size='big' id=7`, map[string]string{"text": "Hello", "size": "big", "id": "7"}},
		"texturized": {` text=&#8221;Hello&#8221; alignment=&#8221;center&#8221;`, map[string]string{"text": "Hello", "alignment": "center"}},
		"inch":       {` size=&#8221;10&#8243; x=&#8221;1&#8221;`, map[string]string{"size": "10", "x": "1"}},
		"unicode":    {" text=\u201dHi\u201d w=\u201c5\u2033", map[string]string{"text": "Hi", "w": "5"}},
		"br":         {` a="1"<br />b="2"`, map[string]string{"a": "1", "b": "2"}},
		"upper+dup":  {` Key="a" key="b"`, map[string]string{"key": "b"}},
		"entity":     {` text=&#8221;A &amp; B&#8221;`, map[string]string{"text": "A &amp; B"}},
	}
	for name, tc := range cases {
		if got := vcAttrs(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
}

func TestMediaByIDAndWithMedia(t *testing.T) {
	m := MediaByID([]Media{{ID: "1", URL: "u1"}, {ID: "2", URL: "u2"}})
	if len(m) != 2 || m["2"].URL != "u2" {
		t.Fatalf("index: %v", m)
	}
	var o convertOpts
	WithMedia(m)(&o)
	if len(o.media) != 2 {
		t.Errorf("WithMedia did not set the index")
	}
}
