package pluginhost

import (
	"context"
	"encoding/json"
	"testing"

	"hatamoto.dev/sdk"
)

// FuzzHostCall feeds arbitrary bytes to hm_call, the one entry point a plugin
// controls. The host must always answer with a well-formed CallResponse that
// holds either a result or an error, whatever the request and its arguments.
func FuzzHostCall(f *testing.F) {
	g := NewGrantStore(nil)
	var perms []sdk.Permission
	for _, fn := range hostFns {
		if fn.perm != "" {
			perms = append(perms, fn.perm)
		}
	}
	if err := g.Set(context.Background(), "p", perms); err != nil {
		f.Fatal(err)
	}
	m := sdk.Manifest{Name: "p"}
	a := NewAPI(Services{Fetcher: NewFetcherForTests()}, g, func(string) (sdk.Manifest, bool) { return m, true }, nil)

	for fn := range hostFns {
		f.Add([]byte(`{"fn":"` + fn + `","args":{}}`))
	}
	f.Add([]byte(`{"fn":"log","args":{"level":"info","message":"hi"}}`))
	f.Add([]byte(`{"fn":"http.fetch","args":{"url":"https://example.test/"}}`))
	f.Add([]byte(`{"fn":"mail.send","args":{"to":["a@example.test"],"subject":"s","text":"t"}}`))
	f.Add([]byte(`{"fn":"kv.get","args":{"key":null}}`))
	f.Add([]byte(`{"fn":"log","args":{"unknown":1}}`))
	f.Add([]byte(`{"fn":1}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, in []byte) {
		var resp sdk.CallResponse
		if err := json.Unmarshal(a.Call(context.Background(), "p", in), &resp); err != nil {
			t.Fatalf("the answer is not a CallResponse: %v", err)
		}
		if resp.OK == (resp.Error != nil) {
			t.Fatalf("exactly one of ok and error must hold: %+v", resp)
		}
	})
}
