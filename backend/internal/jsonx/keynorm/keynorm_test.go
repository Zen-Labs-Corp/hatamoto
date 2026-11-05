package keynorm

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var themeShape = &Shape{Fields: map[string]*Shape{
	"fontFamily":  nil,
	"headerCta":   {Fields: map[string]*Shape{"label": nil, "href": nil}},
	"socialLinks": {Open: true},
	"items":       {Elem: &Shape{Fields: map[string]*Shape{"nodeId": nil}}},
	"limits":      {Open: true, Elem: &Shape{Fields: map[string]*Shape{"maxLen": nil}}},
}}

func norm(t *testing.T, in string, s *Shape) (string, []Change) {
	t.Helper()
	out, ch, err := Normalise([]byte(in), s)
	if err != nil {
		t.Fatalf("Normalise(%s): %v", in, err)
	}
	return string(out), ch
}

func TestRenameTopLevel(t *testing.T) {
	got, ch := norm(t, `{"headercta":{"label":"x"},"other":1}`, themeShape)
	if want := `{"headerCta":{"label":"x"},"other":1}`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if len(ch) != 1 || ch[0].From != "headercta" || ch[0].To != "headerCta" || ch[0].Dropped || ch[0].Path != "" {
		t.Fatalf("changes = %+v", ch)
	}
}

func TestRenameNestedAndArrays(t *testing.T) {
	got, ch := norm(t, `{"HEADERCTA":{"Label":"x","href":"/"},"items":[{"NODEID":"a"},{"nodeId":"b"}],"limits":{"Any":{"MAXLEN":3}}}`, themeShape)
	want := `{"headerCta":{"label":"x","href":"/"},"items":[{"nodeId":"a"},{"nodeId":"b"}],"limits":{"Any":{"maxLen":3}}}`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if len(ch) != 4 {
		t.Fatalf("changes = %+v", ch)
	}
	var found bool
	for _, c := range ch {
		if c.Path == "/items/0" && c.From == "NODEID" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no /items/0 change in %+v", ch)
	}
}

func TestCollision(t *testing.T) {
	// The last fold-matching member wins, as v1 read it, whatever its spelling.
	got, ch := norm(t, `{"fontfamily":"a","fontFamily":"b","FONTFAMILY":"c"}`, themeShape)
	if want := `{"fontFamily":"c"}`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if len(ch) != 3 || ch[0].Dropped || !ch[1].Dropped || !ch[2].Dropped {
		t.Fatalf("changes = %+v", ch)
	}
	if ch[1].Value != `"a"` || ch[2].Value != `"b"` {
		t.Fatalf("dropped values not reported: %+v", ch)
	}
	// A canonical spelling that is not last loses.
	got, _ = norm(t, `{"fontFamily":"b","fontfamily":"a"}`, themeShape)
	if want := `{"fontFamily":"a"}`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	got, ch = norm(t, `{"fontfamily":"a","FONTFAMILY":"c","FontFamily":"d"}`, themeShape)
	if want := `{"fontFamily":"d"}`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if len(ch) != 3 {
		t.Fatalf("changes = %+v", ch)
	}
	if s := ch[1].String(); !strings.Contains(s, `value "a"`) {
		t.Fatalf("String() lacks the dropped value: %s", s)
	}
}

func TestFoldEqualCanonicalNamesAreDeterministic(t *testing.T) {
	shape := &Shape{Fields: map[string]*Shape{"id": nil, "ID": nil, "Id": nil}}
	for range 50 {
		got, _ := norm(t, `{"iD":1}`, shape)
		if want := `{"ID":1}`; got != want {
			t.Fatalf("got %s want %s", got, want)
		}
	}
}

func TestCanonicalIsUnchanged(t *testing.T) {
	in := []byte(`{ "fontFamily" : "x",  "socialLinks":{"Twitter":"a","twitter":"b"} }`)
	out, ch, err := Normalise(in, themeShape)
	if err != nil || len(ch) != 0 {
		t.Fatalf("err %v changes %+v", err, ch)
	}
	if &out[0] != &in[0] {
		t.Fatalf("input not returned as is: %s", out)
	}
}

func TestUnknownKeysAndOpaquePassThrough(t *testing.T) {
	in := `{"builder":{"AnyKey":1},"headercta":{"extra":{"X":1}}}`
	got, _ := norm(t, in, themeShape)
	if want := `{"builder":{"AnyKey":1},"headerCta":{"extra":{"X":1}}}`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestNumbersAndStringsPreserved(t *testing.T) {
	in := `{"headercta":{"label":"\u003cb\u003e"},"big":12345678901234567890123,"exp":1e400,"f":0.10}`
	got, _ := norm(t, in, themeShape)
	want := `{"headerCta":{"label":"\u003cb\u003e"},"big":12345678901234567890123,"exp":1e400,"f":0.10}`
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestIdempotent(t *testing.T) {
	once, _ := norm(t, `{"HEADERCTA":{"LABEL":"x"},"fontfamily":1,"FontFamily":2,"items":[{"NodeId":1}]}`, themeShape)
	twice, ch := norm(t, once, themeShape)
	if once != twice || len(ch) != 0 {
		t.Fatalf("once %s twice %s changes %+v", once, twice, ch)
	}
}

func TestNilShapeAndShapeMismatch(t *testing.T) {
	if got, ch := norm(t, `{"A":1}`, nil); got != `{"A":1}` || ch != nil {
		t.Fatalf("nil shape: %s %v", got, ch)
	}
	// An array where an object is expected, and a scalar where either is.
	if got, ch := norm(t, `{"headercta":[1],"items":"x"}`, themeShape); got != `{"headerCta":[1],"items":"x"}` || len(ch) != 1 {
		t.Fatalf("mismatch: %s %v", got, ch)
	}
	if _, _, err := Normalise([]byte(`{"a":`), themeShape); err == nil {
		t.Fatal("truncated input accepted")
	}
	if got, _ := norm(t, `null`, themeShape); got != `null` {
		t.Fatalf("null: %s", got)
	}
}

type tagged struct {
	Plain   string `json:"plain"`
	NoTag   int
	Omit    string `json:"omit,omitempty"`
	Skipped string `json:"-"`
	//lint:ignore SA5008 the tag is deliberate: "-," names the field "-"
	Dash  string            `json:"-,"`
	When  time.Time         `json:"when"`
	Kids  []child           `json:"kids"`
	Dict  map[string]child  `json:"dict"`
	Ptr   *child            `json:"ptr"`
	Tags  []string          `json:"tags"`
	Attrs map[string]string `json:"attrs"`
	//lint:ignore U1000 unexported on purpose: ShapeOf must skip it
	hidden int
	Base
}

type Base struct {
	FromBase string `json:"fromBase"`
}

type child struct {
	NodeID string `json:"nodeId"`
	Self   *child `json:"self"`
}

func TestShapeOf(t *testing.T) {
	s := ShapeOf(reflect.TypeOf(&tagged{}))
	for _, k := range []string{"plain", "NoTag", "omit", "-", "when", "kids", "dict", "ptr", "tags", "attrs", "fromBase"} {
		if _, ok := s.Fields[k]; !ok {
			t.Errorf("missing field %q in %v", k, s.Fields)
		}
	}
	for _, k := range []string{"Skipped", "hidden", "Base"} {
		if _, ok := s.Fields[k]; ok {
			t.Errorf("unexpected field %q", k)
		}
	}
	if s.Fields["when"] != nil || s.Fields["tags"] != nil || s.Fields["attrs"] != nil {
		t.Errorf("scalar-like fields must be opaque: %+v", s.Fields)
	}
	if k := s.Fields["kids"]; k == nil || k.Elem == nil || k.Elem.Fields["nodeId"] == nil && !hasKey(k.Elem.Fields, "nodeId") {
		t.Errorf("kids shape = %+v", k)
	}
	if d := s.Fields["dict"]; d == nil || !d.Open || d.Elem == nil {
		t.Errorf("dict shape = %+v", d)
	}
	// The recursive field is cut off instead of looping.
	if self := s.Fields["ptr"].Fields["self"]; self != nil {
		t.Errorf("cyclic field should be opaque, got %+v", self)
	}

	got, _ := norm(t, `{"PLAIN":"a","FROMBASE":"b","KIDS":[{"NODEID":"x"}]}`, s)
	if want := `{"plain":"a","fromBase":"b","kids":[{"nodeId":"x"}]}`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func hasKey(m map[string]*Shape, k string) bool { _, ok := m[k]; return ok }

// v1 loaded a document with duplicate names inside array elements (last
// value wins); keynorm must not refuse it.
func TestDuplicateNamesInArrayElements(t *testing.T) {
	got, _ := norm(t, `{"items":[{"nodeId":"a","nodeId":"b"},{"NODEID":"c"}]}`, themeShape)
	if want := `{"items":[{"nodeId":"b"},{"nodeId":"c"}]}`; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
