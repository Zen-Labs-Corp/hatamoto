package searchreplace

import "testing"

// TestWalkBytesPin pins the bytes Walk writes, as the v1 encoder produced
// them: keys sorted, no HTML escaping, numbers verbatim, input returned
// untouched when nothing was replaced.
func TestWalkBytesPin(t *testing.T) {
	all := ScopeContent | ScopeSEO | ScopeOther
	always := func(string, int) bool { return true }
	cases := []struct {
		name string
		in   string
		o    Options
		want string
	}{
		{
			"sorted keys and no html escape",
			`{"title":"old <b> & co","meta_title":"old","body":"<p>old</p>","a":1}`,
			Options{Find: "old", Replace: "new <i>&"},
			`{"a":1,"body":"<p>new &lt;i&gt;&amp;</p>","meta_title":"new <i>&","title":"new <i>& <b> & co"}`,
		},
		{
			"numbers verbatim",
			`{"title":"old","price":1.0,"exp":1e3,"big":9007199254740993,"dec":1.50,"neg":-0.0}`,
			Options{Find: "old", Replace: "new"},
			`{"big":9007199254740993,"dec":1.50,"exp":1e3,"neg":-0.0,"price":1.0,"title":"new"}`,
		},
		{
			"nested sorted",
			`{"title":"old","z":{"y":"old","b":[{"d":2,"c":"old"}]}}`,
			Options{Find: "old", Replace: "new"},
			`{"title":"new","z":{"b":[{"c":"new","d":2}],"y":"new"}}`,
		},
		{
			"line separator and unicode",
			`{"title":"old \u2028 é ☃ \u0001"}`,
			Options{Find: "old", Replace: "new"},
			`{"title":"new \u2028 é ☃ \u0001"}`,
		},
		{
			"nothing replaced keeps input bytes",
			"{ \"title\" : \"keep\",\n \"b\":1.0, \"a\":[1 , 2] }",
			Options{Find: "old", Replace: "new"},
			"{ \"title\" : \"keep\",\n \"b\":1.0, \"a\":[1 , 2] }",
		},
		{
			"hit without a match keeps input bytes",
			"{ \"title\" : \"old\" }",
			Options{Find: "zzz", Replace: "new"},
			"{ \"title\" : \"old\" }",
		},
		{
			"array root untouched",
			`[ "old" , {"title":"old"} ]`,
			Options{Find: "old", Replace: "new"},
			`[ "old" , {"title":"old"} ]`,
		},
		{
			"scalar root untouched",
			`"old"`,
			Options{Find: "old", Replace: "new"},
			`"old"`,
		},
		{
			"null root untouched",
			`null`,
			Options{Find: "old", Replace: "new"},
			`null`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, _, _, err := Walk([]byte(c.in), c.o, all, nil, always)
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != c.want {
				t.Errorf("got  %s\nwant %s", out, c.want)
			}
		})
	}
}
