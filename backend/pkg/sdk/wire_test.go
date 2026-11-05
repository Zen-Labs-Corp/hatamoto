package sdk

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// The expectations below are string literals written against the
// encoding/json v1 encoder, before the SDK moved to JSON v2. They pin the
// bytes of every wire type the SDK defines, in four states: the zero value, a
// value with every field set, and "edge" values, where pointers point at zero
// structs, slices and maps are empty and raw JSON members are {}, null or "".
// Edge values are where omitempty and omitzero differ. They must not change
// with the encoder.
//
// The values are built by fill, a reflection helper, so the table stays a list
// of names and literals.

type wireFill int

const (
	wireZero wireFill = iota
	wireFull
	wireEdge
	wireEdgeNull
	wireEdgeStr
)

var rawType = reflect.TypeOf(json.RawMessage(nil))
var timeType = reflect.TypeOf(time.Time{})

func wireFillValue(v reflect.Value, m wireFill, depth int) {
	t := v.Type()
	switch {
	case t == timeType:
		if m != wireZero {
			v.Set(reflect.ValueOf(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)))
		}
		return
	case t == rawType:
		switch m {
		case wireFull:
			v.Set(reflect.ValueOf(json.RawMessage(`{"a":1}`)))
		case wireEdge:
			v.Set(reflect.ValueOf(json.RawMessage(`{}`)))
		case wireEdgeNull:
			v.Set(reflect.ValueOf(json.RawMessage(`null`)))
		case wireEdgeStr:
			v.Set(reflect.ValueOf(json.RawMessage(`""`)))
		}
		return
	}
	if m == wireZero {
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).IsExported() {
				wireFillValue(v.Field(i), m, depth)
			}
		}
	case reflect.Pointer:
		if depth > 2 {
			return
		}
		p := reflect.New(t.Elem())
		wireFillValue(p.Elem(), m, depth+1)
		v.Set(p)
	case reflect.String:
		if m == wireFull {
			v.SetString("s<")
		}
	case reflect.Int, reflect.Int64, reflect.Int32:
		if m == wireFull {
			v.SetInt(1)
		}
	case reflect.Uint, reflect.Uint64, reflect.Uint32:
		if m == wireFull {
			v.SetUint(1)
		}
	case reflect.Bool:
		if m == wireFull {
			v.SetBool(true)
		}
	case reflect.Float64:
		if m == wireFull {
			v.SetFloat(1.5)
		}
	case reflect.Interface:
		if m == wireFull {
			v.Set(reflect.ValueOf("i"))
		}
	case reflect.Slice:
		s := reflect.MakeSlice(t, 0, 1)
		if m == wireFull && depth <= 2 {
			e := reflect.New(t.Elem()).Elem()
			wireFillValue(e, m, depth+1)
			s = reflect.Append(s, e)
		}
		v.Set(s)
	case reflect.Map:
		mp := reflect.MakeMap(t)
		if m == wireFull && depth <= 2 {
			e := reflect.New(t.Elem()).Elem()
			wireFillValue(e, m, depth+1)
			k := reflect.New(t.Key()).Elem()
			k.SetString("k")
			mp.SetMapIndex(k, e)
		}
		v.Set(mp)
	}
}

func wireHasRaw(t reflect.Type, seen map[reflect.Type]bool) bool {
	if t == rawType {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if wireHasRaw(t.Field(i).Type, seen) {
				return true
			}
		}
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return wireHasRaw(t.Elem(), seen)
	}
	return false
}

func wireTypes() map[string]reflect.Type {
	m := map[string]reflect.Type{}
	for _, v := range []any{EntityRef{}, EntityEvent{}, ImportEvent{}, HeadContext{}, SiteGraph{}, HeadTag{}, AgentHitEvent{}, AgentFirstCrawlEvent{}, JSONLDDoc{}, Alternate{}, SitemapURL{}, SitemapURLs{}, EntityDraft{}, FormSubmittedEvent{}, FormTouch{}, FormAttribution{}, PageContext{}, EntitySummary{}, Block{}, SetCookie{}, BuilderDataRequest{}, BuilderDataQuery{}, BuilderDataResult{}, BuilderDataItem{},
		CallRequest{}, CallResponse{}, HostError{}, HTTPRequest{}, HTTPUser{}, HTTPResponse{}, CronEvent{},
		Limits{}, Manifest{}, CSP{}, BuilderSource{}, PageOptions{}, Route{}, AdminUI{}, CronJob{},
		Run{}, StandingApproval{}, RunInput{}, RunStep{}, RunStepResult{}, RunInfo{}, RunCheckpointArgs{}, RunProgressArgs{}, RunFinishArgs{}, RunFailArgs{}, UploadReadArgs{}, UploadChunk{}, BufferPutArgs{}, BufferGetArgs{}, BufferValue{}, BufferListArgs{}, BufferKeys{}, MediaImportURLArgs{}, MediaHandle{}, SiteInfo{}, SlugTakenArgs{}, SlugTaken{}, SEO{}, EntityUpsert{}, TermUpsert{}, PersonUpsert{}, MediaAttach{}, MediaAlt{}, MenuUpsert{}, MenuItem{}, RedirectUpsert{}, ChangesetDeleteArgs{}, UpsertResult{}, Proposal{}} {
		t := reflect.TypeOf(v)
		m[t.Name()] = t
	}
	return m
}

var wireCases = []struct{ typ, state, want string }{
	{"EntityRef", "zero", `{"id":"","schema_slug":"","slug":"","locale":"","name":"","status":"","version":0}`},
	{"EntityRef", "full", `{"id":"s\u003c","schema_slug":"s\u003c","slug":"s\u003c","locale":"s\u003c","name":"s\u003c","status":"s\u003c","version":1}`},
	{"EntityRef", "edge", `{"id":"","schema_slug":"","slug":"","locale":"","name":"","status":"","version":0}`},
	{"EntityEvent", "zero", `{"id":"","schema_slug":"","slug":"","locale":"","name":"","status":"","version":0}`},
	{"EntityEvent", "full", `{"id":"s\u003c","schema_slug":"s\u003c","slug":"s\u003c","locale":"s\u003c","name":"s\u003c","status":"s\u003c","version":1}`},
	{"EntityEvent", "edge", `{"id":"","schema_slug":"","slug":"","locale":"","name":"","status":"","version":0}`},
	{"ImportEvent", "zero", `{"source":"","created":0,"updated":0,"failed":0}`},
	{"ImportEvent", "full", `{"source":"s\u003c","created":1,"updated":1,"failed":1}`},
	{"ImportEvent", "edge", `{"source":"","created":0,"updated":0,"failed":0}`},
	{"HeadContext", "zero", `{"path":"","language":"","schema_slug":"","entity_id":"","replace_site_graph":false}`},
	{"HeadContext", "full", `{"path":"s\u003c","language":"s\u003c","schema_slug":"s\u003c","entity_id":"s\u003c","locale":"s\u003c","segment":"s\u003c","tags":[{"kind":"s\u003c","attrs":{"k":"s\u003c"},"jsonld":{"a":1}}],"replace_site_graph":true}`},
	{"HeadContext", "edge", `{"path":"","language":"","schema_slug":"","entity_id":"","replace_site_graph":false}`},
	{"HeadContext", "edge null", `{"path":"","language":"","schema_slug":"","entity_id":"","replace_site_graph":false}`},
	{"HeadContext", "edge empty string", `{"path":"","language":"","schema_slug":"","entity_id":"","replace_site_graph":false}`},
	{"SiteGraph", "zero", `{"path":"","language":"","schema_slug":"","entity_id":"","nodes":null}`},
	{"SiteGraph", "full", `{"path":"s\u003c","language":"s\u003c","schema_slug":"s\u003c","entity_id":"s\u003c","locale":"s\u003c","segment":"s\u003c","nodes":[{"k":"i"}]}`},
	{"SiteGraph", "edge", `{"path":"","language":"","schema_slug":"","entity_id":"","nodes":[]}`},
	{"HeadTag", "zero", `{"kind":""}`},
	{"HeadTag", "full", `{"kind":"s\u003c","attrs":{"k":"s\u003c"},"jsonld":{"a":1}}`},
	{"HeadTag", "edge", `{"kind":"","jsonld":{}}`},
	{"HeadTag", "edge null", `{"kind":"","jsonld":null}`},
	{"HeadTag", "edge empty string", `{"kind":"","jsonld":""}`},
	{"AgentHitEvent", "zero", `{"agent_id":"","operator":"","purpose":"","verification":"","method":"","path":"","entity_id":"","source":"","status":0,"bytes":0,"duration_ms":0,"occurred_at":"0001-01-01T00:00:00Z"}`},
	{"AgentHitEvent", "full", `{"agent_id":"s\u003c","operator":"s\u003c","purpose":"s\u003c","verification":"s\u003c","method":"s\u003c","path":"s\u003c","entity_id":"s\u003c","source":"s\u003c","status":1,"bytes":1,"duration_ms":1,"occurred_at":"2024-01-02T03:04:05Z"}`},
	{"AgentHitEvent", "edge", `{"agent_id":"","operator":"","purpose":"","verification":"","method":"","path":"","entity_id":"","source":"","status":0,"bytes":0,"duration_ms":0,"occurred_at":"2024-01-02T03:04:05Z"}`},
	{"AgentFirstCrawlEvent", "zero", `{"entity_id":"","agent_id":"","operator":"","purpose":"","first_at":"0001-01-01T00:00:00Z"}`},
	{"AgentFirstCrawlEvent", "full", `{"entity_id":"s\u003c","agent_id":"s\u003c","operator":"s\u003c","purpose":"s\u003c","first_at":"2024-01-02T03:04:05Z","published_at":"2024-01-02T03:04:05Z"}`},
	{"AgentFirstCrawlEvent", "edge", `{"entity_id":"","agent_id":"","operator":"","purpose":"","first_at":"2024-01-02T03:04:05Z","published_at":"2024-01-02T03:04:05Z"}`},
	{"JSONLDDoc", "zero", `{"entity":{"id":"","schema_slug":"","slug":"","locale":"","name":"","status":"","version":0},"base_url":"","doc":null}`},
	{"JSONLDDoc", "full", `{"entity":{"id":"s\u003c","schema_slug":"s\u003c","slug":"s\u003c","locale":"s\u003c","name":"s\u003c","status":"s\u003c","version":1},"base_url":"s\u003c","doc":{"k":"i"}}`},
	{"JSONLDDoc", "edge", `{"entity":{"id":"","schema_slug":"","slug":"","locale":"","name":"","status":"","version":0},"base_url":"","doc":{}}`},
	{"Alternate", "zero", `{"rel":"","hreflang":"","href":""}`},
	{"Alternate", "full", `{"rel":"s\u003c","hreflang":"s\u003c","href":"s\u003c"}`},
	{"Alternate", "edge", `{"rel":"","hreflang":"","href":""}`},
	{"SitemapURL", "zero", `{"loc":"","lastmod":"","changefreq":"","priority":0}`},
	{"SitemapURL", "full", `{"loc":"s\u003c","lastmod":"s\u003c","changefreq":"s\u003c","priority":1.5,"alternates":[{"rel":"s\u003c","hreflang":"s\u003c","href":"s\u003c"}]}`},
	{"SitemapURL", "edge", `{"loc":"","lastmod":"","changefreq":"","priority":0}`},
	{"SitemapURLs", "zero", `{"group":"","language":"","add":null}`},
	{"SitemapURLs", "full", `{"group":"s\u003c","language":"s\u003c","segment":"s\u003c","file":"s\u003c","add":[{"loc":"s\u003c","lastmod":"s\u003c","changefreq":"s\u003c","priority":1.5,"alternates":[{"rel":"s\u003c","hreflang":"s\u003c","href":"s\u003c"}]}]}`},
	{"SitemapURLs", "edge", `{"group":"","language":"","add":[]}`},
	{"EntityDraft", "zero", `{"operation":"","id":"","schema_slug":"","name":"","slug":"","attributes":null}`},
	{"EntityDraft", "full", `{"operation":"s\u003c","id":"s\u003c","schema_slug":"s\u003c","name":"s\u003c","slug":"s\u003c","locale":"s\u003c","attributes":{"a":1}}`},
	{"EntityDraft", "edge", `{"operation":"","id":"","schema_slug":"","name":"","slug":"","attributes":{}}`},
	{"EntityDraft", "edge null", `{"operation":"","id":"","schema_slug":"","name":"","slug":"","attributes":null}`},
	{"EntityDraft", "edge empty string", `{"operation":"","id":"","schema_slug":"","name":"","slug":"","attributes":""}`},
	{"FormSubmittedEvent", "zero", `{"form_id":"","form_slug":"","submission_id":"","fields":null,"submitted_at":"0001-01-01T00:00:00Z"}`},
	{"FormSubmittedEvent", "full", `{"form_id":"s\u003c","form_slug":"s\u003c","submission_id":"s\u003c","fields":{"k":"i"},"source_path":"s\u003c","submitted_at":"2024-01-02T03:04:05Z","attribution":{"first":{"channel":"s\u003c","source":"s\u003c","medium":"s\u003c","campaign":"s\u003c","landing":"s\u003c"},"last":{"channel":"s\u003c","source":"s\u003c","medium":"s\u003c","campaign":"s\u003c","landing":"s\u003c"}}}`},
	{"FormSubmittedEvent", "edge", `{"form_id":"","form_slug":"","submission_id":"","fields":{},"submitted_at":"2024-01-02T03:04:05Z","attribution":{"first":{},"last":{}}}`},
	{"FormTouch", "zero", `{}`},
	{"FormTouch", "full", `{"channel":"s\u003c","source":"s\u003c","medium":"s\u003c","campaign":"s\u003c","landing":"s\u003c"}`},
	{"FormTouch", "edge", `{}`},
	{"FormAttribution", "zero", `{}`},
	{"FormAttribution", "full", `{"first":{"channel":"s\u003c","source":"s\u003c","medium":"s\u003c","campaign":"s\u003c","landing":"s\u003c"},"last":{"channel":"s\u003c","source":"s\u003c","medium":"s\u003c","campaign":"s\u003c","landing":"s\u003c"}}`},
	{"FormAttribution", "edge", `{"first":{},"last":{}}`},
	{"PageContext", "zero", `{"path":"","cookies":null,"blocks":null}`},
	{"PageContext", "full", `{"path":"s\u003c","cookies":{"k":"s\u003c"},"entity":{"id":"s\u003c","schema_slug":"s\u003c","slug":"s\u003c","locale":"s\u003c","name":"s\u003c"},"blocks":[{"id":"s\u003c","type":"s\u003c","title":"s\u003c","fields":{"k":"i"},"content":{"k":"i"},"hidden":true}],"hide":["s\u003c"],"set_cookies":[{"name":"s\u003c","value":"s\u003c","max_age":1,"path":"s\u003c"}]}`},
	{"PageContext", "edge", `{"path":"","cookies":{},"entity":{"id":"","schema_slug":"","slug":"","locale":"","name":""},"blocks":[]}`},
	{"EntitySummary", "zero", `{"id":"","schema_slug":"","slug":"","locale":"","name":""}`},
	{"EntitySummary", "full", `{"id":"s\u003c","schema_slug":"s\u003c","slug":"s\u003c","locale":"s\u003c","name":"s\u003c"}`},
	{"EntitySummary", "edge", `{"id":"","schema_slug":"","slug":"","locale":"","name":""}`},
	{"Block", "zero", `{"id":"","type":""}`},
	{"Block", "full", `{"id":"s\u003c","type":"s\u003c","title":"s\u003c","fields":{"k":"i"},"content":{"k":"i"},"hidden":true}`},
	{"Block", "edge", `{"id":"","type":""}`},
	{"SetCookie", "zero", `{"name":"","value":""}`},
	{"SetCookie", "full", `{"name":"s\u003c","value":"s\u003c","max_age":1,"path":"s\u003c"}`},
	{"SetCookie", "edge", `{"name":"","value":""}`},
	{"BuilderDataRequest", "zero", `{}`},
	{"BuilderDataRequest", "full", `{"path":"s\u003c","language":"s\u003c","schema_slug":"s\u003c","entity_id":"s\u003c","requests":[{"id":"s\u003c","source":"s\u003c","limit":1}],"results":[{"id":"s\u003c","value":"s\u003c","items":[{"title":"s\u003c","url":"s\u003c","excerpt":"s\u003c","image":"s\u003c","category":"s\u003c","author":"s\u003c","author_url":"s\u003c","published_at":"s\u003c","attrs":{"k":"s\u003c"}}],"error":"s\u003c"}]}`},
	{"BuilderDataRequest", "edge", `{}`},
	{"BuilderDataQuery", "zero", `{"id":"","source":""}`},
	{"BuilderDataQuery", "full", `{"id":"s\u003c","source":"s\u003c","limit":1}`},
	{"BuilderDataQuery", "edge", `{"id":"","source":""}`},
	{"BuilderDataResult", "zero", `{"id":""}`},
	{"BuilderDataResult", "full", `{"id":"s\u003c","value":"s\u003c","items":[{"title":"s\u003c","url":"s\u003c","excerpt":"s\u003c","image":"s\u003c","category":"s\u003c","author":"s\u003c","author_url":"s\u003c","published_at":"s\u003c","attrs":{"k":"s\u003c"}}],"error":"s\u003c"}`},
	{"BuilderDataResult", "edge", `{"id":""}`},
	{"BuilderDataItem", "zero", `{}`},
	{"BuilderDataItem", "full", `{"title":"s\u003c","url":"s\u003c","excerpt":"s\u003c","image":"s\u003c","category":"s\u003c","author":"s\u003c","author_url":"s\u003c","published_at":"s\u003c","attrs":{"k":"s\u003c"}}`},
	{"BuilderDataItem", "edge", `{}`},
	{"CallRequest", "zero", `{"fn":""}`},
	{"CallRequest", "full", `{"fn":"s\u003c","args":{"a":1}}`},
	{"CallRequest", "edge", `{"fn":"","args":{}}`},
	{"CallRequest", "edge null", `{"fn":"","args":null}`},
	{"CallRequest", "edge empty string", `{"fn":"","args":""}`},
	{"CallResponse", "zero", `{"ok":false}`},
	{"CallResponse", "full", `{"ok":true,"result":{"a":1},"error":{"code":"s\u003c","capability":"s\u003c","message":"s\u003c"}}`},
	{"CallResponse", "edge", `{"ok":false,"result":{},"error":{"code":"","message":""}}`},
	{"CallResponse", "edge null", `{"ok":false,"result":null,"error":{"code":"","message":""}}`},
	{"CallResponse", "edge empty string", `{"ok":false,"result":"","error":{"code":"","message":""}}`},
	{"HostError", "zero", `{"code":"","message":""}`},
	{"HostError", "full", `{"code":"s\u003c","capability":"s\u003c","message":"s\u003c"}`},
	{"HostError", "edge", `{"code":"","message":""}`},
	{"HTTPRequest", "zero", `{"method":"","path":""}`},
	{"HTTPRequest", "full", `{"method":"s\u003c","path":"s\u003c","pattern":"s\u003c","path_values":{"k":"s\u003c"},"query":{"k":["s\u003c"]},"headers":{"k":"s\u003c"},"body":"s\u003c","body_b64":"s\u003c","user":{"id":"s\u003c","role":"s\u003c"},"cookies":{"k":"s\u003c"}}`},
	{"HTTPRequest", "edge", `{"method":"","path":"","user":{"id":"","role":""}}`},
	{"HTTPUser", "zero", `{"id":"","role":""}`},
	{"HTTPUser", "full", `{"id":"s\u003c","role":"s\u003c"}`},
	{"HTTPUser", "edge", `{"id":"","role":""}`},
	{"HTTPResponse", "zero", `{}`},
	{"HTTPResponse", "full", `{"status":1,"headers":{"k":"s\u003c"},"body":"s\u003c","body_b64":"s\u003c","set_cookies":[{"name":"s\u003c","value":"s\u003c","max_age":1,"path":"s\u003c"}]}`},
	{"HTTPResponse", "edge", `{}`},
	{"CronEvent", "zero", `{"name":"","scheduled_at":""}`},
	{"CronEvent", "full", `{"name":"s\u003c","scheduled_at":"s\u003c"}`},
	{"CronEvent", "edge", `{"name":"","scheduled_at":""}`},
	{"Limits", "zero", `{}`},
	{"Limits", "full", `{"memory_pages":1,"hook_timeouts_ms":{"k":1},"max_instances":1,"max_output_bytes":1}`},
	{"Limits", "edge", `{}`},
	{"Manifest", "zero", `{"name":"","version":"","sdk":"","limits":{}}`},
	{"Manifest", "full", `{"name":"s\u003c","version":"s\u003c","sdk":"s\u003c","description":"s\u003c","config":{"k":"s\u003c"},"allowed_hosts":["s\u003c"],"permissions":["s\u003c"],"routes":[{"method":"s\u003c","path":"s\u003c","public":true,"roles":["s\u003c"]}],"cron":[{"name":"s\u003c","every":"s\u003c"}],"cookies":["s\u003c"],"request_headers":["s\u003c"],"csp":{"frame_src":["s\u003c"],"img_src":["s\u003c"],"script_src":["s\u003c"],"connect_src":["s\u003c"]},"render_page":{"full_blocks":true},"funcs":["s\u003c"],"ui":{"title":"s\u003c","entry":"s\u003c","roles":["s\u003c"]},"builder_sources":[{"name":"s\u003c","label":"s\u003c","kind":"s\u003c","class":"s\u003c"}],"runs":[{"name":"s\u003c","title":"s\u003c","inputs":[{"name":"s\u003c","label":"s\u003c","type":"s\u003c","required":true,"options":["s\u003c"]}],"stages":["s\u003c"],"proposes":["s\u003c"],"cron":"s\u003c","standing_approval":{"fields":["s\u003c"],"schemas":["s\u003c"],"max_per_hour":1}}],"limits":{"memory_pages":1,"hook_timeouts_ms":{"k":1},"max_instances":1,"max_output_bytes":1},"settings_schema":{"a":1}}`},
	{"Manifest", "edge", `{"name":"","version":"","sdk":"","csp":{},"render_page":{},"ui":{"title":""},"limits":{},"settings_schema":{}}`},
	{"Manifest", "edge null", `{"name":"","version":"","sdk":"","csp":{},"render_page":{},"ui":{"title":""},"limits":{},"settings_schema":null}`},
	{"Manifest", "edge empty string", `{"name":"","version":"","sdk":"","csp":{},"render_page":{},"ui":{"title":""},"limits":{},"settings_schema":""}`},
	{"CSP", "zero", `{}`},
	{"CSP", "full", `{"frame_src":["s\u003c"],"img_src":["s\u003c"],"script_src":["s\u003c"],"connect_src":["s\u003c"]}`},
	{"CSP", "edge", `{}`},
	{"BuilderSource", "zero", `{"name":"","label":"","kind":""}`},
	{"BuilderSource", "full", `{"name":"s\u003c","label":"s\u003c","kind":"s\u003c","class":"s\u003c"}`},
	{"BuilderSource", "edge", `{"name":"","label":"","kind":""}`},
	{"PageOptions", "zero", `{}`},
	{"PageOptions", "full", `{"full_blocks":true}`},
	{"PageOptions", "edge", `{}`},
	{"Route", "zero", `{"method":"","path":""}`},
	{"Route", "full", `{"method":"s\u003c","path":"s\u003c","public":true,"roles":["s\u003c"]}`},
	{"Route", "edge", `{"method":"","path":""}`},
	{"AdminUI", "zero", `{"title":""}`},
	{"AdminUI", "full", `{"title":"s\u003c","entry":"s\u003c","roles":["s\u003c"]}`},
	{"AdminUI", "edge", `{"title":""}`},
	{"CronJob", "zero", `{"name":"","every":""}`},
	{"CronJob", "full", `{"name":"s\u003c","every":"s\u003c"}`},
	{"CronJob", "edge", `{"name":"","every":""}`},
	{"Run", "zero", `{"name":"","title":"","stages":null}`},
	{"Run", "full", `{"name":"s\u003c","title":"s\u003c","inputs":[{"name":"s\u003c","label":"s\u003c","type":"s\u003c","required":true,"options":["s\u003c"]}],"stages":["s\u003c"],"proposes":["s\u003c"],"cron":"s\u003c","standing_approval":{"fields":["s\u003c"],"schemas":["s\u003c"],"max_per_hour":1}}`},
	{"Run", "edge", `{"name":"","title":"","stages":[],"standing_approval":{"fields":[],"schemas":[]}}`},
	{"StandingApproval", "zero", `{"fields":null,"schemas":null}`},
	{"StandingApproval", "full", `{"fields":["s\u003c"],"schemas":["s\u003c"],"max_per_hour":1}`},
	{"StandingApproval", "edge", `{"fields":[],"schemas":[]}`},
	{"RunInput", "zero", `{"name":"","label":""}`},
	{"RunInput", "full", `{"name":"s\u003c","label":"s\u003c","type":"s\u003c","required":true,"options":["s\u003c"]}`},
	{"RunInput", "edge", `{"name":"","label":""}`},
	{"RunStep", "zero", `{"run_id":"","phase":"","cursor":""}`},
	{"RunStep", "full", `{"run_id":"s\u003c","phase":"s\u003c","cursor":"s\u003c"}`},
	{"RunStep", "edge", `{"run_id":"","phase":"","cursor":""}`},
	{"RunStepResult", "zero", `{}`},
	{"RunStepResult", "full", `{}`},
	{"RunStepResult", "edge", `{}`},
	{"RunInfo", "zero", `{"run_id":"","name":"","phase":"","cursor":""}`},
	{"RunInfo", "full", `{"run_id":"s\u003c","name":"s\u003c","phase":"s\u003c","cursor":"s\u003c","params":{"a":1},"hosts":["s\u003c"],"upload_id":"s\u003c"}`},
	{"RunInfo", "edge", `{"run_id":"","name":"","phase":"","cursor":"","params":{}}`},
	{"RunInfo", "edge null", `{"run_id":"","name":"","phase":"","cursor":"","params":null}`},
	{"RunInfo", "edge empty string", `{"run_id":"","name":"","phase":"","cursor":"","params":""}`},
	{"RunCheckpointArgs", "zero", `{"cursor":""}`},
	{"RunCheckpointArgs", "full", `{"cursor":"s\u003c"}`},
	{"RunCheckpointArgs", "edge", `{"cursor":""}`},
	{"RunProgressArgs", "zero", `{"done":0}`},
	{"RunProgressArgs", "full", `{"done":1,"total":1,"message":"s\u003c"}`},
	{"RunProgressArgs", "edge", `{"done":0}`},
	{"RunFinishArgs", "zero", `{}`},
	{"RunFinishArgs", "full", `{"summary":{"a":1}}`},
	{"RunFinishArgs", "edge", `{"summary":{}}`},
	{"RunFinishArgs", "edge null", `{"summary":null}`},
	{"RunFinishArgs", "edge empty string", `{"summary":""}`},
	{"RunFailArgs", "zero", `{"reason":""}`},
	{"RunFailArgs", "full", `{"reason":"s\u003c"}`},
	{"RunFailArgs", "edge", `{"reason":""}`},
	{"UploadReadArgs", "zero", `{"upload_id":"","offset":0,"len":0}`},
	{"UploadReadArgs", "full", `{"upload_id":"s\u003c","offset":1,"len":1}`},
	{"UploadReadArgs", "edge", `{"upload_id":"","offset":0,"len":0}`},
	{"UploadChunk", "zero", `{"data_b64":"","size":0,"eof":false}`},
	{"UploadChunk", "full", `{"data_b64":"s\u003c","size":1,"eof":true}`},
	{"UploadChunk", "edge", `{"data_b64":"","size":0,"eof":false}`},
	{"BufferPutArgs", "zero", `{"key":"","value_b64":""}`},
	{"BufferPutArgs", "full", `{"key":"s\u003c","value_b64":"s\u003c"}`},
	{"BufferPutArgs", "edge", `{"key":"","value_b64":""}`},
	{"BufferGetArgs", "zero", `{"key":""}`},
	{"BufferGetArgs", "full", `{"key":"s\u003c"}`},
	{"BufferGetArgs", "edge", `{"key":""}`},
	{"BufferValue", "zero", `{"found":false}`},
	{"BufferValue", "full", `{"found":true,"value_b64":"s\u003c"}`},
	{"BufferValue", "edge", `{"found":false}`},
	{"BufferListArgs", "zero", `{}`},
	{"BufferListArgs", "full", `{"prefix":"s\u003c","after":"s\u003c","limit":1}`},
	{"BufferListArgs", "edge", `{}`},
	{"BufferKeys", "zero", `{"keys":null}`},
	{"BufferKeys", "full", `{"keys":["s\u003c"],"next":"s\u003c"}`},
	{"BufferKeys", "edge", `{"keys":[]}`},
	{"MediaImportURLArgs", "zero", `{"url":""}`},
	{"MediaImportURLArgs", "full", `{"url":"s\u003c","filename":"s\u003c"}`},
	{"MediaImportURLArgs", "edge", `{"url":""}`},
	{"MediaHandle", "zero", `{"handle":""}`},
	{"MediaHandle", "full", `{"handle":"s\u003c"}`},
	{"MediaHandle", "edge", `{"handle":""}`},
	{"SiteInfo", "zero", `{"title":"","time_zone":"","language":""}`},
	{"SiteInfo", "full", `{"title":"s\u003c","tagline":"s\u003c","time_zone":"s\u003c","language":"s\u003c","locales":["s\u003c"],"url_patterns":["s\u003c"]}`},
	{"SiteInfo", "edge", `{"title":"","time_zone":"","language":""}`},
	{"SlugTakenArgs", "zero", `{"schema":"","slug":""}`},
	{"SlugTakenArgs", "full", `{"schema":"s\u003c","slug":"s\u003c","locale":"s\u003c"}`},
	{"SlugTakenArgs", "edge", `{"schema":"","slug":""}`},
	{"SlugTaken", "zero", `{"taken":false}`},
	{"SlugTaken", "full", `{"taken":true}`},
	{"SlugTaken", "edge", `{"taken":false}`},
	{"SEO", "zero", `{}`},
	{"SEO", "full", `{"meta_title":"s\u003c","meta_description":"s\u003c","canonical":"s\u003c","robots":"s\u003c"}`},
	{"SEO", "edge", `{}`},
	{"EntityUpsert", "zero", `{"source_key":"","schema":"","name":""}`},
	{"EntityUpsert", "full", `{"source_key":"s\u003c","schema":"s\u003c","slug":"s\u003c","parent_key":"s\u003c","locale":"s\u003c","translation_of":"s\u003c","name":"s\u003c","attributes":{"a":1},"seo":{"meta_title":"s\u003c","meta_description":"s\u003c","canonical":"s\u003c","robots":"s\u003c"}}`},
	{"EntityUpsert", "edge", `{"source_key":"","schema":"","name":"","attributes":{},"seo":{}}`},
	{"EntityUpsert", "edge null", `{"source_key":"","schema":"","name":"","attributes":null,"seo":{}}`},
	{"EntityUpsert", "edge empty string", `{"source_key":"","schema":"","name":"","attributes":"","seo":{}}`},
	{"TermUpsert", "zero", `{"source_key":"","kind":"","slug":"","name":""}`},
	{"TermUpsert", "full", `{"source_key":"s\u003c","kind":"s\u003c","slug":"s\u003c","name":"s\u003c","description":"s\u003c"}`},
	{"TermUpsert", "edge", `{"source_key":"","kind":"","slug":"","name":""}`},
	{"PersonUpsert", "zero", `{"source_key":"","slug":"","name":""}`},
	{"PersonUpsert", "full", `{"source_key":"s\u003c","slug":"s\u003c","name":"s\u003c","bio":"s\u003c"}`},
	{"PersonUpsert", "edge", `{"source_key":"","slug":"","name":""}`},
	{"MediaAttach", "zero", `{"source_key":"","handle":""}`},
	{"MediaAttach", "full", `{"source_key":"s\u003c","handle":"s\u003c","alt":"s\u003c","caption":"s\u003c","credit":"s\u003c"}`},
	{"MediaAttach", "edge", `{"source_key":"","handle":""}`},
	{"MediaAlt", "zero", `{"media_id":"","alt":""}`},
	{"MediaAlt", "full", `{"media_id":"s\u003c","alt":"s\u003c"}`},
	{"MediaAlt", "edge", `{"media_id":"","alt":""}`},
	{"MenuUpsert", "zero", `{"source_key":"","slug":"","name":""}`},
	{"MenuUpsert", "full", `{"source_key":"s\u003c","slug":"s\u003c","name":"s\u003c","items":[{"label":"s\u003c","url":"s\u003c","entity_key":"s\u003c","children":[{"label":"s\u003c","url":"s\u003c","entity_key":"s\u003c","children":[{"label":"s\u003c","url":"s\u003c","entity_key":"s\u003c"}]}]}]}`},
	{"MenuUpsert", "edge", `{"source_key":"","slug":"","name":""}`},
	{"MenuItem", "zero", `{"label":""}`},
	{"MenuItem", "full", `{"label":"s\u003c","url":"s\u003c","entity_key":"s\u003c","children":[{"label":"s\u003c","url":"s\u003c","entity_key":"s\u003c","children":[{"label":"s\u003c","url":"s\u003c","entity_key":"s\u003c","children":[{"label":"s\u003c","url":"s\u003c","entity_key":"s\u003c"}]}]}]}`},
	{"MenuItem", "edge", `{"label":""}`},
	{"RedirectUpsert", "zero", `{"source_key":"","from":"","to":""}`},
	{"RedirectUpsert", "full", `{"source_key":"s\u003c","from":"s\u003c","to":"s\u003c","status_code":1}`},
	{"RedirectUpsert", "edge", `{"source_key":"","from":"","to":""}`},
	{"ChangesetDeleteArgs", "zero", `{"kind":"","source_key":""}`},
	{"ChangesetDeleteArgs", "full", `{"kind":"s\u003c","source_key":"s\u003c"}`},
	{"ChangesetDeleteArgs", "edge", `{"kind":"","source_key":""}`},
	{"UpsertResult", "zero", `{"source_key":"","created":false}`},
	{"UpsertResult", "full", `{"source_key":"s\u003c","created":true}`},
	{"UpsertResult", "edge", `{"source_key":"","created":false}`},
	{"Proposal", "zero", `{"kind":"","payload":null}`},
	{"Proposal", "full", `{"kind":"s\u003c","payload":{"a":1}}`},
	{"Proposal", "edge", `{"kind":"","payload":{}}`},
	{"Proposal", "edge null", `{"kind":"","payload":null}`},
	{"Proposal", "edge empty string", `{"kind":"","payload":""}`},
}

func TestWireSDKTypes(t *testing.T) {
	types := wireTypes()
	seen := map[string]bool{}
	for _, c := range wireCases {
		rt, ok := types[c.typ]
		if !ok {
			t.Fatalf("unknown type %s", c.typ)
		}
		seen[c.typ] = true
		mode := map[string]wireFill{"zero": wireZero, "full": wireFull, "edge": wireEdge, "edge null": wireEdgeNull, "edge empty string": wireEdgeStr}[c.state]
		p := reflect.New(rt)
		wireFillValue(p.Elem(), mode, 0)
		got, err := json.Marshal(p.Interface())
		if err != nil {
			t.Fatalf("%s %s: %v", c.typ, c.state, err)
		}
		if string(got) != c.want {
			t.Errorf("%s %s:\n got  %s\n want %s", c.typ, c.state, got, c.want)
		}
	}
	for name, rt := range types {
		if !seen[name] {
			t.Errorf("%s has no wire case", rt)
		}
	}
}

// How the SDK reads what a host or an author writes: names without regard to
// case, the last of a repeated name, null members that leave the zero value
// and invalid UTF-8 read as U+FFFD.
func TestWireSDKRead(t *testing.T) {
	var m Manifest
	in := `{"NAME":"a","name":"b","Version":"1.0.0","sdk":"^0.6","description":"x` + "\xff" + `y",` +
		`"limits":null,"routes":null,"permissions":["kv",null],"config":{"K":null},"csp":{"FRAME_SRC":["a"]}}`
	if err := json.Unmarshal([]byte(in), &m); err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"name":"b","version":"1.0.0","sdk":"^0.6","description":"x` + "\xef\xbf\xbd" + `y","config":{"K":""},"permissions":["kv",""],"csp":{"frame_src":["a"]},"limits":{}}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}

	var ev FormSubmittedEvent
	if err := json.Unmarshal([]byte(`{"FORM_ID":"f","form_id":"g","fields":null,"submitted_at":"2024-01-02T03:04:05Z","attribution":null}`), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.FormID != "g" || ev.Fields != nil || ev.Attribution != nil || !ev.SubmittedAt.Equal(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("event = %+v", ev)
	}

	var req CallRequest
	if err := json.Unmarshal([]byte(`{"FN":"x","Args":null}`), &req); err != nil || req.Fn != "x" || string(req.Args) != "null" {
		t.Fatalf("request = %+v %v", req, err)
	}
	var resp CallResponse
	if err := json.Unmarshal([]byte(`{"OK":true,"Result":{"a":1},"error":null}`), &resp); err != nil || !resp.OK || string(resp.Result) != `{"a":1}` || resp.Error != nil {
		t.Fatalf("response = %+v %v", resp, err)
	}
}
