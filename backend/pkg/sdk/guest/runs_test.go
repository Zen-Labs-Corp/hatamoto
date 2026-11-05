package guest

import (
	"encoding/base64"
	"encoding/json/jsontext"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// fakeRunHost answers host calls from a table and records the last request,
// through the same Invoke path the wasm guest uses.
type fakeRunHost struct {
	fn      string
	args    jsontext.Value
	results map[string]string // fn -> result JSON
	errs    map[string]*sdk.HostError
}

func (f *fakeRunHost) caller() Caller {
	return func(fn string, args, out any) error {
		return Invoke(func(req []byte) ([]byte, error) {
			var r sdk.CallRequest
			if err := unmarshal(req, &r); err != nil {
				return nil, err
			}
			f.fn, f.args = r.Fn, r.Args
			if he := f.errs[r.Fn]; he != nil {
				return marshal(sdk.CallResponse{Error: he})
			}
			res := f.results[r.Fn]
			if res == "" {
				res = "{}"
			}
			return marshal(sdk.CallResponse{OK: true, Result: jsontext.Value(res)})
		}, fn, args, out)
	}
}

func (f *fakeRunHost) sent(t *testing.T, wantFn string, wantArgs string) {
	t.Helper()
	if f.fn != wantFn || string(f.args) != wantArgs {
		t.Fatalf("host saw %s %s, want %s %s", f.fn, f.args, wantFn, wantArgs)
	}
}

func TestOnRunStep(t *testing.T) {
	OnRunStep(func(s sdk.RunStep) (sdk.RunStepResult, error) {
		if s.Phase != sdk.RunPhaseFetch || s.Cursor != "c1" {
			t.Errorf("step = %+v", s)
		}
		return sdk.RunStepResult{}, nil
	})
	out, err := Handle(sdk.RunStepExport, []byte(`{"run_id":"r","phase":"fetch","cursor":"c1"}`))
	if err != nil || string(out) != "{}" {
		t.Fatalf("Handle: %s %v", out, err)
	}
	if _, err := Handle(sdk.RunStepExport, []byte(`nope`)); err == nil {
		t.Fatal("a bad step payload must error")
	}
}

func TestRunFunctions(t *testing.T) {
	h := &fakeRunHost{results: map[string]string{
		sdk.FnRunInfo: `{"run_id":"r1","phase":"fetch","hosts":["example.com"]}`,
	}}
	call := h.caller()

	info, err := RunInfo(call)
	if err != nil || info.RunID != "r1" || info.Hosts[0] != "example.com" {
		t.Fatalf("RunInfo: %+v %v", info, err)
	}
	if err := RunCheckpoint(call, "page=2"); err != nil {
		t.Fatal(err)
	}
	h.sent(t, sdk.FnRunCheckpoint, `{"cursor":"page=2"}`)
	if err := RunProgress(call, 3, 10, "posts"); err != nil {
		t.Fatal(err)
	}
	h.sent(t, sdk.FnRunProgress, `{"done":3,"total":10,"message":"posts"}`)
	if err := RunFinish(call, map[string]int{"posts": 3}); err != nil {
		t.Fatal(err)
	}
	h.sent(t, sdk.FnRunFinish, `{"summary":{"posts":3}}`)
	if err := RunFinish(call, nil); err != nil {
		t.Fatal(err)
	}
	h.sent(t, sdk.FnRunFinish, `{}`)
	if err := RunFail(call, "bad export"); err != nil {
		t.Fatal(err)
	}
	h.sent(t, sdk.FnRunFail, `{"reason":"bad export"}`)
}

func TestUploadAndBuffer(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("hello"))
	h := &fakeRunHost{results: map[string]string{
		sdk.FnUploadRead:     `{"data_b64":"` + b64 + `","size":5,"eof":true}`,
		sdk.FnBufferGet:      `{"found":true,"value_b64":"` + b64 + `"}`,
		sdk.FnBufferList:     `{"keys":["a/1","a/2"],"next":"a/2"}`,
		sdk.FnMediaImportURL: `{"handle":"staged/r/1"}`,
	}}
	call := h.caller()

	data, eof, err := UploadRead(call, "u1", 0, 5)
	if err != nil || string(data) != "hello" || !eof {
		t.Fatalf("UploadRead: %q %v %v", data, eof, err)
	}
	h.sent(t, sdk.FnUploadRead, `{"upload_id":"u1","offset":0,"len":5}`)

	if err := BufferPut(call, "k", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	h.sent(t, sdk.FnBufferPut, `{"key":"k","value_b64":"`+b64+`"}`)

	v, found, err := BufferGet(call, "k")
	if err != nil || !found || string(v) != "hello" {
		t.Fatalf("BufferGet: %q %v %v", v, found, err)
	}
	keys, next, err := BufferList(call, "a/", "", 2)
	if err != nil || len(keys) != 2 || next != "a/2" {
		t.Fatalf("BufferList: %v %q %v", keys, next, err)
	}
	handle, err := MediaImportURL(call, "https://example.com/a.png", "a.png")
	if err != nil || handle != "staged/r/1" {
		t.Fatalf("MediaImportURL: %q %v", handle, err)
	}

	h.results[sdk.FnBufferGet] = `{"found":false}`
	if v, found, err := BufferGet(call, "gone"); err != nil || found || v != nil {
		t.Fatalf("missing key: %q %v %v", v, found, err)
	}
}

func TestSiteAndChangesetFunctions(t *testing.T) {
	h := &fakeRunHost{results: map[string]string{
		sdk.FnSiteInfo:              `{"title":"T","time_zone":"UTC","language":"en"}`,
		sdk.FnEntitiesSlugTaken:     `{"taken":true}`,
		sdk.FnChangesetEntityUpsert: `{"source_key":"p1","created":true}`,
	}}
	call := h.caller()

	if info, err := SiteInfo(call); err != nil || info.Title != "T" {
		t.Fatalf("SiteInfo: %+v %v", info, err)
	}
	if taken, err := SlugTaken(call, "page", "about", ""); err != nil || !taken {
		t.Fatalf("SlugTaken: %v %v", taken, err)
	}
	res, err := EntityUpsert(call, sdk.EntityUpsert{SourceKey: "p1", Schema: "page", Name: "About", ParentKey: "p0", SEO: &sdk.SEO{MetaTitle: "About us"}})
	if err != nil || !res.Created || res.SourceKey != "p1" {
		t.Fatalf("EntityUpsert: %+v %v", res, err)
	}
	h.sent(t, sdk.FnChangesetEntityUpsert, `{"source_key":"p1","schema":"page","parent_key":"p0","name":"About","seo":{"meta_title":"About us"}}`)

	for name, c := range map[string]struct {
		do func() error
		fn string
	}{
		"term": {func() error {
			_, err := TermUpsert(call, sdk.TermUpsert{SourceKey: "t", Kind: "category", Slug: "news", Name: "News"})
			return err
		}, sdk.FnChangesetTermUpsert},
		"person": {func() error {
			_, err := PersonUpsert(call, sdk.PersonUpsert{SourceKey: "a", Slug: "ann", Name: "Ann"})
			return err
		}, sdk.FnChangesetPersonUpsert},
		"media": {func() error { _, err := MediaAttach(call, sdk.MediaAttach{SourceKey: "m", Handle: "h"}); return err }, sdk.FnChangesetMediaAttach},
		"menu": {func() error {
			_, err := MenuUpsert(call, sdk.MenuUpsert{SourceKey: "n", Slug: "main", Name: "Main"})
			return err
		}, sdk.FnChangesetMenuUpsert},
		"redirect": {func() error {
			_, err := RedirectUpsert(call, sdk.RedirectUpsert{SourceKey: "r", From: "/a", To: "/b"})
			return err
		}, sdk.FnChangesetRedirectUpsert},
		"delete":  {func() error { _, err := Delete(call, "entity", "p1"); return err }, sdk.FnChangesetDelete},
		"propose": {func() error { return Propose(call, sdk.ProposeSiteSettings, map[string]string{"title": "T"}) }, sdk.FnChangesetPropose},
	} {
		if err := c.do(); err != nil || h.fn != c.fn {
			t.Errorf("%s: %v, host saw %q, want %q", name, err, h.fn, c.fn)
		}
	}
	if err := Propose(call, sdk.ProposeSiteSettings, map[string]string{"title": "T"}); err != nil {
		t.Fatal(err)
	}
	h.sent(t, sdk.FnChangesetPropose, `{"kind":"site_settings","payload":{"title":"T"}}`)
}

func TestPhaseDeniedAndNotOwned(t *testing.T) {
	h := &fakeRunHost{errs: map[string]*sdk.HostError{
		sdk.FnBufferGet:             {Code: sdk.ErrPhaseDenied, Message: "buffer.get is not available in the fetch phase"},
		sdk.FnChangesetEntityUpsert: {Code: sdk.ErrNotOwned, Message: "object belongs to the owner"},
	}}
	call := h.caller()

	_, _, err := BufferGet(call, "k")
	if !PhaseDenied(err) || NotOwned(err) || !strings.Contains(err.Error(), "fetch phase") {
		t.Fatalf("BufferGet error = %v", err)
	}
	_, err = EntityUpsert(call, sdk.EntityUpsert{SourceKey: "x", Schema: "page", Name: "X"})
	if !NotOwned(err) || PhaseDenied(err) {
		t.Fatalf("EntityUpsert error = %v", err)
	}
	if PhaseDenied(nil) || NotOwned(nil) {
		t.Fatal("nil is neither")
	}
}
