package plugmanifest

import (
	"reflect"
	"testing"

	"hatamoto.dev/sdk"
)

func TestDecode(t *testing.T) {
	const base = `"name":"hello","version":"1.0.0","sdk":"^0.7"`
	for name, tc := range map[string]struct {
		in   string
		want func(sdk.Manifest) any
		val  any
	}{
		"case variant name":   {`{"NAME":"hello","version":"1.0.0"}`, func(m sdk.Manifest) any { return m.Name }, "hello"},
		"underscore variant":  {`{` + base + `,"allowedHosts":["evil.example"]}`, func(m sdk.Manifest) any { return len(m.AllowedHosts) }, 0},
		"dash variant":        {`{` + base + `,"request-headers":["x-a"]}`, func(m sdk.Manifest) any { return len(m.RequestHeaders) }, 0},
		"exact spelling":      {`{` + base + `,"allowed_hosts":["a.example"]}`, func(m sdk.Manifest) any { return m.AllowedHosts }, []string{"a.example"}},
		"case duplicate wins": {`{` + base + `,"allowed_hosts":["a.example"],"ALLOWED_HOSTS":["b.example"]}`, func(m sdk.Manifest) any { return m.AllowedHosts }, []string{"b.example"}},
		"invalid utf8":        {"{" + base + ",\"description\":\"caf\xe9\"}", func(m sdk.Manifest) any { return m.Description }, "caf\ufffd"},
	} {
		t.Run(name, func(t *testing.T) {
			var m sdk.Manifest
			if err := Decode([]byte(tc.in), &m); err != nil {
				t.Fatal(err)
			}
			if got := tc.want(m); !reflect.DeepEqual(got, tc.val) {
				t.Fatalf("got %#v, want %#v", got, tc.val)
			}
		})
	}
}
