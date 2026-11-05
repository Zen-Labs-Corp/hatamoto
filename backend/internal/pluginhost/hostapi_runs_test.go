package pluginhost

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/sdk"
)

// runAPI is an API with no run store, for the functions that need none.
func runAPI(t *testing.T, svc Services) *API {
	t.Helper()
	g := NewGrantStore(nil)
	return NewAPI(svc, g, func(string) (sdk.Manifest, bool) { return sdk.Manifest{Name: "p"}, true }, nil)
}

// callRun calls fn as plugin p from inside a run in phase.
func callRun(t *testing.T, a *API, rc RunCtx, fn string, args any) (jsontext.Value, *sdk.HostError) {
	t.Helper()
	raw, _ := jsonx.MarshalPinned(args)
	in, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: fn, Args: raw})
	var resp sdk.CallResponse
	if err := jsonx.UnmarshalPinned(a.Call(WithRun(context.Background(), rc), "p", in), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		return nil, resp.Error
	}
	return resp.Result, nil
}

func newRun(phase string, hosts ...string) RunCtx {
	return RunCtx{RunID: uuid.New(), Plugin: "p", Name: "import", Phase: phase, Hosts: hosts, Signal: &RunSignal{}}
}

type fakeBlobs struct {
	runID uuid.UUID
	name  string
	data  []byte
}

func (f *fakeBlobs) StageBlob(_ context.Context, runID uuid.UUID, r io.Reader, filename string) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	f.runID, f.name, f.data = runID, filename, b
	return "staged/" + runID.String() + "/" + filename, nil
}

type fakeSite struct{}

func (fakeSite) Info(context.Context) (sdk.SiteInfo, error) {
	return sdk.SiteInfo{Title: "T", TimeZone: "UTC", Language: "en"}, nil
}

func TestRunFnsFinishAndFail(t *testing.T) {
	a := runAPI(t, Services{})
	rc := newRun(sdk.RunPhaseFetch)
	if _, he := callRun(t, a, rc, sdk.FnRunFinish, map[string]any{"summary": map[string]int{"n": 2}}); he != nil {
		t.Fatal(he)
	}
	if fin, fail, sum := rc.Signal.Get(); !fin || fail != "" || string(sum) != `{"n":2}` {
		t.Fatalf("finish signal: %v %q %s", fin, fail, sum)
	}
	rc = newRun(sdk.RunPhaseTransform)
	if _, he := callRun(t, a, rc, sdk.FnRunFail, map[string]any{"reason": "  "}); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("blank reason: %v", he)
	}
	if _, he := callRun(t, a, rc, sdk.FnRunFail, map[string]any{"reason": "bad feed"}); he != nil {
		t.Fatal(he)
	}
	if fin, fail, _ := rc.Signal.Get(); fin || fail != "bad feed" {
		t.Fatalf("fail signal: %v %q", fin, fail)
	}
	big := strings.Repeat("x", maxRunSummary+1)
	if _, he := callRun(t, a, newRun(sdk.RunPhaseFetch), sdk.FnRunFinish, map[string]any{"summary": big}); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("oversize summary: %v", he)
	}
	if _, he := callRun(t, a, newRun(sdk.RunPhaseFetch), sdk.FnRunFinish, map[string]any{"nope": 1}); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("unknown field: %v", he)
	}
}

func TestRunFnsOutsideRunRefused(t *testing.T) {
	a := runAPI(t, Services{Site: fakeSite{}})
	in, _ := jsonx.MarshalPinned(sdk.CallRequest{Fn: sdk.FnSiteInfo, Args: jsontext.Value(`{}`)})
	var resp sdk.CallResponse
	_ = jsonx.UnmarshalPinned(a.Call(context.Background(), "p", in), &resp)
	if resp.OK || resp.Error.Code != sdk.ErrPhaseDenied {
		t.Fatalf("site.info outside a run: %+v", resp)
	}
}

func TestRunFnsSiteInfoAndSlugTaken(t *testing.T) {
	rc := newRun(sdk.RunPhaseTransform)
	var asked []string
	a := runAPI(t, Services{Site: fakeSite{}, SlugTaken: func(_ context.Context, schema, slug, locale string) (bool, error) {
		asked = []string{schema, slug, locale}
		return slug == "taken", nil
	}})
	res, he := callRun(t, a, rc, sdk.FnSiteInfo, map[string]any{})
	if he != nil || !strings.Contains(string(res), `"title":"T"`) {
		t.Fatalf("site.info: %s %v", res, he)
	}
	res, he = callRun(t, a, rc, sdk.FnEntitiesSlugTaken, sdk.SlugTakenArgs{Schema: "post", Slug: "taken", Locale: "fr"})
	if he != nil || string(res) != `{"taken":true}` || strings.Join(asked, ",") != "post,taken,fr" {
		t.Fatalf("slug_taken: %s %v %v", res, he, asked)
	}
	if _, he := callRun(t, a, rc, sdk.FnEntitiesSlugTaken, sdk.SlugTakenArgs{Schema: "post"}); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("missing slug: %v", he)
	}
	// Without the services the functions are unavailable, not a crash.
	bare := runAPI(t, Services{})
	for _, fn := range []string{sdk.FnSiteInfo, sdk.FnEntitiesSlugTaken} {
		if _, he := callRun(t, bare, rc, fn, map[string]any{"schema": "a", "slug": "b"}); he == nil || he.Code != sdk.ErrUnavailable {
			t.Fatalf("%s without service: %v", fn, he)
		}
	}
}

func TestRunFnsMediaImportURL(t *testing.T) {
	payload := bytes.Repeat([]byte("p"), 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing.png":
			http.NotFound(w, r)
		case "/big.bin":
			w.Write(bytes.Repeat([]byte("b"), maxImportBytes+1))
		default:
			w.Write(payload)
		}
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	host := "127.0.0.1:" + port

	blobs := &fakeBlobs{}
	a := runAPI(t, Services{Fetcher: NewFetcherForTests(), Blobs: blobs})
	rc := newRun(sdk.RunPhaseFetch, host)

	res, he := callRun(t, a, rc, sdk.FnMediaImportURL, sdk.MediaImportURLArgs{URL: srv.URL + "/dir/photo.jpg"})
	if he != nil {
		t.Fatal(he)
	}
	var h sdk.MediaHandle
	_ = jsonx.UnmarshalPinned(res, &h)
	if blobs.runID != rc.RunID || blobs.name != "photo.jpg" || !bytes.Equal(blobs.data, payload) || !strings.HasSuffix(h.Handle, "/photo.jpg") {
		t.Fatalf("staged %v %q %d bytes, handle %q", blobs.runID, blobs.name, len(blobs.data), h.Handle)
	}

	// A host the owner did not confirm is blocked.
	if _, he := callRun(t, a, newRun(sdk.RunPhaseFetch, "other.example"), sdk.FnMediaImportURL, sdk.MediaImportURLArgs{URL: srv.URL + "/x.jpg"}); he == nil || he.Code != sdk.ErrBlocked {
		t.Fatalf("unconfirmed host: %v", he)
	}
	// A wildcard host needs the http.fetch.any grant.
	if _, he := callRun(t, a, newRun(sdk.RunPhaseFetch, "*:"+port), sdk.FnMediaImportURL, sdk.MediaImportURLArgs{URL: srv.URL + "/x.jpg"}); he == nil || he.Code != sdk.ErrBlocked {
		t.Fatalf("wildcard without grant: %v", he)
	}
	if _, he := callRun(t, a, rc, sdk.FnMediaImportURL, sdk.MediaImportURLArgs{URL: srv.URL + "/missing.png"}); he == nil || he.Code != sdk.ErrUnavailable {
		t.Fatalf("404: %v", he)
	}
	if _, he := callRun(t, a, rc, sdk.FnMediaImportURL, sdk.MediaImportURLArgs{URL: srv.URL + "/big.bin"}); he == nil || he.Code != sdk.ErrInvalidArgument || !strings.Contains(he.Message, "over 64 MiB") {
		t.Fatalf("oversize: %v", he)
	}
	if _, he := callRun(t, a, rc, sdk.FnMediaImportURL, sdk.MediaImportURLArgs{URL: srv.URL + "/", Filename: "a/b.png"}); he == nil || he.Code != sdk.ErrInvalidArgument {
		t.Fatalf("path as filename: %v", he)
	}
	// Transform phase: the gate refuses before any download.
	if _, he := callRun(t, a, newRun(sdk.RunPhaseTransform, host), sdk.FnMediaImportURL, sdk.MediaImportURLArgs{URL: srv.URL + "/x.jpg"}); he == nil || he.Code != sdk.ErrPhaseDenied {
		t.Fatalf("transform phase: %v", he)
	}
}

func TestImportFilename(t *testing.T) {
	for _, tc := range []struct{ given, url, want string }{
		{"", "https://e.com/a/b.png?x=1", "b.png"},
		{"c.png", "https://e.com/a/b.png", "c.png"},
		{"", "https://e.com/", ""},
		{"../x.png", "https://e.com/a.png", ""},
		{"", "https://e.com", ""},
	} {
		if got := importFilename(tc.given, tc.url); got != tc.want {
			t.Errorf("importFilename(%q, %q) = %q, want %q", tc.given, tc.url, got, tc.want)
		}
	}
}
