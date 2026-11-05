package guest

import (
	"encoding/json"
	"encoding/json/jsontext"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// The expectations in this file are string literals written against the
// encoding/json v1 implementation: the bytes a guest writes to the host (and
// reads from it) must not change with the encoder.

func TestWireGuestOutputs(t *testing.T) {
	var action sdk.FormSubmittedEvent
	OnAction(sdk.FormSubmitted, func(ev sdk.FormSubmittedEvent) error { action = ev; return nil })
	OnFilter(sdk.RenderPage, func(p sdk.PageContext) (sdk.PageContext, error) {
		if p.Path == "/set" {
			p.Hide = append(p.Hide, "a<")
			p.SetCookies = append(p.SetCookies, sdk.SetCookie{Name: "n", Value: "v"}, sdk.SetCookie{Name: "m", Value: "w", MaxAge: 5, Path: "/p"})
			p.Blocks = []sdk.Block{{ID: "b", Type: "t", Hidden: false}, {ID: "c", Type: "u", Title: "T&", Content: map[string]any{"z": 1, "a": []string{}}}}
		}
		return p, nil
	})
	OnHTTP(func(req sdk.HTTPRequest) (sdk.HTTPResponse, error) {
		switch req.Path {
		case "/full":
			return sdk.HTTPResponse{Status: 201, Headers: map[string]string{"b": "<", "a": "x"}, Body: "é<", BodyB64: "eA==",
				SetCookies: []sdk.SetCookie{{Name: "n", Value: "v", MaxAge: 60}}}, nil
		}
		return sdk.HTTPResponse{}, nil
	})
	var cron sdk.CronEvent
	OnCron("wire", func(ev sdk.CronEvent) error { cron = ev; return nil })
	var args []string
	OnFunc("wire", func(a []json.RawMessage) (string, error) {
		args = nil
		for _, v := range a {
			args = append(args, string(v))
		}
		return "<b>é", nil
	})

	handle := func(export, in string) string {
		t.Helper()
		out, err := Handle(export, []byte(in))
		if err != nil {
			t.Fatalf("%s %s: %v", export, in, err)
		}
		return string(out)
	}

	// filter results
	for _, c := range []struct{ in, want string }{
		{`{}`, `{"path":"","cookies":null,"blocks":null}`},
		{`{"path":"/p","cookies":{"b":"2","a":"1"},"blocks":[{"id":"x","type":"y"}],"hide":null,"set_cookies":null}`,
			`{"path":"/p","cookies":{"a":"1","b":"2"},"blocks":[{"id":"x","type":"y"}]}`},
		{`{"path":"/set","cookies":{}}`,
			`{"path":"/set","cookies":{},"blocks":[{"id":"b","type":"t"},{"id":"c","type":"u","title":"T\u0026","content":{"a":[],"z":1}}],"hide":["a\u003c"],"set_cookies":[{"name":"n","value":"v"},{"name":"m","value":"w","max_age":5,"path":"/p"}]}`},
		{`{"PATH":"/q","Path":"/r"}`, `{"path":"/r","cookies":null,"blocks":null}`},
		{`{"path":"a` + "\xff" + `b"}`, `{"path":"a` + "\xef\xbf\xbd" + `b","cookies":null,"blocks":null}`},
	} {
		if got := handle("filter_render_page", c.in); got != c.want {
			t.Errorf("filter %s:\n got  %s\n want %s", c.in, got, c.want)
		}
	}

	// route results
	for _, c := range []struct{ in, want string }{
		{`{"method":"GET","path":"/none"}`, `{}`},
		{`{"METHOD":"GET","path":"/full"}`, `{"status":201,"headers":{"a":"x","b":"\u003c"},"body":"é\u003c","body_b64":"eA==","set_cookies":[{"name":"n","value":"v","max_age":60}]}`},
	} {
		if got := handle(HTTPExport, c.in); got != c.want {
			t.Errorf("route %s:\n got  %s\n want %s", c.in, got, c.want)
		}
	}

	// template func: the raw arguments reach the handler as written, the text
	// comes back as is.
	if got := handle(FuncExport("wire"), ` [ 1 ,"a<",{"b" : 2}, null,[ ] ] `); got != "<b>é" {
		t.Errorf("func output = %q", got)
	}
	if got, want := strings.Join(args, "|"), `1|"a<"|{"b" : 2}|null|[ ]`; got != want {
		t.Errorf("func args = %s, want %s", got, want)
	}
	if _, err := Handle(FuncExport("wire"), []byte(`{"a":1}`)); err == nil {
		t.Error("func args that are not an array were accepted")
	}

	// actions and events: what the guest reads from the host
	if got := handle(sdk.FormSubmitted.Export(),
		`{"FORM_ID":"f1","form_id":"f2","form_slug":"a`+"\xff"+`b","fields":{"k":null,"k":2},"submitted_at":"2024-01-02T03:04:05Z","source_path":null,"attribution":null}`); got != "" {
		t.Errorf("action output = %q", got)
	}
	if action.FormID != "f2" || action.FormSlug != "a\ufffdb" || action.Fields["k"] != float64(2) || action.Attribution != nil || action.SourcePath != "" {
		t.Errorf("action event = %+v", action)
	}
	handle(CronExport("wire"), `{"NAME":"tick","scheduled_at":"2024-01-02T03:04:05Z"}`)
	if cron.Name != "tick" || cron.ScheduledAt != "2024-01-02T03:04:05Z" {
		t.Errorf("cron event = %+v", cron)
	}
	if _, err := Handle(sdk.FormSubmitted.Export(), []byte(`{"form_id":1}`)); err == nil {
		t.Error("a payload of the wrong type was accepted")
	}
}

func TestWireGuestCallRequests(t *testing.T) {
	var sent string
	call := func(resp string) func([]byte) ([]byte, error) {
		return func(in []byte) ([]byte, error) { sent = string(in); return []byte(resp), nil }
	}
	type kv struct {
		Key   string `json:"key"`
		Value any    `json:"value,omitempty"`
		TTL   int    `json:"ttl_seconds,omitempty"`
	}
	for _, c := range []struct {
		name string
		args any
		want string
	}{
		{"nil args", nil, `{"fn":"x","args":null}`},
		{"empty struct", struct{}{}, `{"fn":"x","args":{}}`},
		{"struct", kv{Key: "k<"}, `{"fn":"x","args":{"key":"k\u003c"}}`},
		{"struct with value", kv{Key: "k", Value: map[string]any{"b": 1, "a": []int(nil)}, TTL: 5}, `{"fn":"x","args":{"key":"k","value":{"a":null,"b":1},"ttl_seconds":5}}`},
		{"map", map[string]string{"b": "2", "a": "1"}, `{"fn":"x","args":{"a":"1","b":"2"}}`},
		{"nil slice", []string(nil), `{"fn":"x","args":null}`},
		{"raw", json.RawMessage(" {\"a\" : 1 } "), `{"fn":"x","args":{"a":1}}`},
		{"invalid UTF-8", "a\xffb", `{"fn":"x","args":"a` + "\xef\xbf\xbd" + `b"}`},
		{"line separator", "a\u2028", `{"fn":"x","args":"a\u2028"}`},
	} {
		if err := Invoke(call(`{"ok":true}`), "x", c.args, nil); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if sent != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, sent, c.want)
		}
	}

	// responses
	var out struct {
		A int
		B []string
	}
	if err := Invoke(call(`{"OK":true,"RESULT":{"a":1,"A":2,"b":null}}`), "x", nil, &out); err != nil || out.A != 2 || out.B != nil {
		t.Errorf("case variants and duplicates: %+v %v", out, err)
	}
	out.A = 9
	if err := Invoke(call(`{"ok":true,"result":null}`), "x", nil, &out); err != nil || out.A != 9 {
		t.Errorf("null result: %+v %v", out, err)
	}
	err := Invoke(call(`{"ok":false,"error":{"CODE":"capability_denied","capability":"kv","message":"a`+"\xff"+`"}}`), "x", nil, nil)
	if c, ok := CapabilityDenied(err); !ok || c != "kv" || !strings.Contains(err.Error(), "a\ufffd") {
		t.Errorf("typed error = %v", err)
	}
	if err := Invoke(call(`{"ok":false,"error":null}`), "x", nil, nil); err == nil || !strings.Contains(err.Error(), "host call failed") {
		t.Errorf("error response without error = %v", err)
	}
	if err := Invoke(call(`nope`), "x", nil, nil); err == nil {
		t.Error("an unreadable response was accepted")
	}
}

// The arguments of the run and changeset functions the guest sends.
func TestWireGuestRunArgs(t *testing.T) {
	h := &fakeRunHost{}
	call := h.caller()
	check := func(name string, err error, fn, want string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if h.fn != fn || string(h.args) != want {
			t.Errorf("%s:\n got  %s %s\n want %s %s", name, h.fn, h.args, fn, want)
		}
	}
	check("progress zero", RunProgress(call, 0, 0, ""), sdk.FnRunProgress, `{"done":0}`)
	check("checkpoint empty", RunCheckpoint(call, ""), sdk.FnRunCheckpoint, `{"cursor":""}`)
	check("finish summary", RunFinish(call, map[string]any{"b": "<", "a": nil}), sdk.FnRunFinish, `{"summary":{"a":null,"b":"\u003c"}}`)
	check("finish empty summary", RunFinish(call, struct{}{}), sdk.FnRunFinish, `{"summary":{}}`)
	check("fail", RunFail(call, "a\xffb"), sdk.FnRunFail, `{"reason":"a`+"\xef\xbf\xbd"+`b"}`)
	check("info", func() error { _, err := RunInfo(call); return err }(), sdk.FnRunInfo, `{}`)
	check("buffer list", func() error { _, _, err := BufferList(call, "", "", 0); return err }(), sdk.FnBufferList, `{}`)
	check("slug taken", func() error { _, err := SlugTaken(call, "s", "x", ""); return err }(), sdk.FnEntitiesSlugTaken, `{"schema":"s","slug":"x"}`)

	_, err := EntityUpsert(call, sdk.EntityUpsert{SourceKey: "k", Schema: "post", Name: "N"})
	check("entity upsert", err, sdk.FnChangesetEntityUpsert, `{"source_key":"k","schema":"post","name":"N"}`)
	_, err = EntityUpsert(call, sdk.EntityUpsert{SourceKey: "k", Schema: "post", Slug: "s", Name: "N", Attributes: json.RawMessage(`{"b": 1,"a":"<"}`), SEO: &sdk.SEO{}})
	check("entity upsert full", err, sdk.FnChangesetEntityUpsert, `{"source_key":"k","schema":"post","slug":"s","name":"N","attributes":{"b":1,"a":"\u003c"},"seo":{}}`)
	_, err = TermUpsert(call, sdk.TermUpsert{SourceKey: "k", Kind: "tag", Slug: "s", Name: "N"})
	check("term", err, sdk.FnChangesetTermUpsert, `{"source_key":"k","kind":"tag","slug":"s","name":"N"}`)
	_, err = PersonUpsert(call, sdk.PersonUpsert{SourceKey: "k", Slug: "s", Name: "N", Bio: "b"})
	check("person", err, sdk.FnChangesetPersonUpsert, `{"source_key":"k","slug":"s","name":"N","bio":"b"}`)
	_, err = MediaAttach(call, sdk.MediaAttach{SourceKey: "k", Handle: "h"})
	check("media attach", err, sdk.FnChangesetMediaAttach, `{"source_key":"k","handle":"h"}`)
	_, err = MediaAlt(call, sdk.MediaAlt{MediaID: "m"})
	check("media alt", err, sdk.FnChangesetMediaAlt, `{"media_id":"m","alt":""}`)
	_, err = MenuUpsert(call, sdk.MenuUpsert{SourceKey: "k", Slug: "s", Name: "N", Items: []sdk.MenuItem{{Label: "l", Children: []sdk.MenuItem{{Label: "c", URL: "/u"}}}}})
	check("menu", err, sdk.FnChangesetMenuUpsert, `{"source_key":"k","slug":"s","name":"N","items":[{"label":"l","children":[{"label":"c","url":"/u"}]}]}`)
	_, err = RedirectUpsert(call, sdk.RedirectUpsert{SourceKey: "k", From: "/a", To: "/b"})
	check("redirect", err, sdk.FnChangesetRedirectUpsert, `{"source_key":"k","from":"/a","to":"/b"}`)
	_, err = RedirectUpsert(call, sdk.RedirectUpsert{SourceKey: "k", From: "/a", To: "/b", StatusCode: 301})
	check("redirect with status", err, sdk.FnChangesetRedirectUpsert, `{"source_key":"k","from":"/a","to":"/b","status_code":301}`)
	_, err = Delete(call, "entity", "k")
	check("delete", err, sdk.FnChangesetDelete, `{"kind":"entity","source_key":"k"}`)
	check("propose", Propose(call, "site_settings", map[string]string{"title": "<T>"}), sdk.FnChangesetPropose, `{"kind":"site_settings","payload":{"title":"\u003cT\u003e"}}`)
	check("propose nil", Propose(call, "site_settings", nil), sdk.FnChangesetPropose, `{"kind":"site_settings","payload":null}`)
}

// Plugin-author types go through the guest encoder too (call arguments, run
// summaries), so they must keep their v1 bytes: omitempty drops empty
// containers, a time.Duration is its nanosecond count, a byte array is a
// number array and a raw value may repeat member names.
func TestWireGuestAuthorTypes(t *testing.T) {
	type author struct {
		N int               `json:"n,omitempty"`
		B bool              `json:"b,omitempty"`
		S string            `json:"s,omitempty"`
		M map[string]int    `json:"m,omitempty"`
		L []int             `json:"l,omitempty"`
		D time.Duration     `json:"d"`
		A [2]byte           `json:"a"`
		V jsontext.Value    `json:"v,omitempty"`
		P *struct{}         `json:"p,omitempty"`
		X map[string]string `json:"x"`
	}
	for _, c := range []struct {
		name string
		v    any
		want string
	}{
		{"zero values omitted", author{}, `{"d":0,"a":[0,0],"x":null}`},
		{"empty containers omitted", author{M: map[string]int{}, L: []int{}, V: jsontext.Value{}}, `{"d":0,"a":[0,0],"x":null}`},
		{"set values", author{N: 3, B: true, S: "s", M: map[string]int{"b": 1}, L: []int{1}, D: time.Second, A: [2]byte{1, 2}, P: &struct{}{}},
			`{"n":3,"b":true,"s":"s","m":{"b":1},"l":[1],"d":1000000000,"a":[1,2],"p":{},"x":null}`},
		{"duplicate names in a raw value", author{V: jsontext.Value(`{"a":1,"a":{"b":1,"b":2}}`)}, `{"d":0,"a":[0,0],"v":{"a":1,"a":{"b":1,"b":2}},"x":null}`},
	} {
		got, err := marshal(c.v)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}

	// A non-nil empty raw value in an SDK field was omitted by v1.
	empty := jsontext.Value{}
	for _, c := range []struct {
		name string
		v    any
		want string
	}{
		{"entity attributes", sdk.EntityUpsert{SourceKey: "k", Schema: "post", Name: "N", Attributes: empty}, `{"source_key":"k","schema":"post","name":"N"}`},
		{"head tag jsonld", sdk.HeadTag{Kind: "jsonld", JSONLD: empty}, `{"kind":"jsonld"}`},
		{"run finish summary", sdk.RunFinishArgs{Summary: empty}, `{}`},
		{"call request args", sdk.CallRequest{Fn: "x", Args: empty}, `{"fn":"x"}`},
	} {
		got, err := marshal(c.v)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}
}

// TestWireGuestReadsAuthorTypes decodes the bytes v1 wrote for author types.
func TestWireGuestReadsAuthorTypes(t *testing.T) {
	type author struct {
		D time.Duration `json:"d"`
		A [2]byte       `json:"a"`
		F bool          `json:"f,string"`
		U int           `json:"user_id"`
	}
	var got author
	if err := unmarshal([]byte(`{"d":1500000000,"a":[1,2],"f":"true","USER_ID":7}`), &got); err != nil {
		t.Fatal(err)
	}
	if want := (author{D: 1500 * time.Millisecond, A: [2]byte{1, 2}, F: true, U: 7}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	var d struct{ D time.Duration }
	if err := unmarshal([]byte(`{"D":1000000000}`), &d); err != nil || d.D != time.Second {
		t.Errorf("duration: %v %v", d, err)
	}
	var a struct{ A [2]byte }
	if err := unmarshal([]byte(`{"A":[9,8]}`), &a); err != nil || a.A != [2]byte{9, 8} {
		t.Errorf("array: %v %v", a, err)
	}
}
