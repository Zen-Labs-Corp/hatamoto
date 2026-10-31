package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

type fakeRegistrar struct {
	fns  map[string]func(context.Context, any)
	prio map[string]int
}

func (f *fakeRegistrar) AddAction(name string, priority int, fn func(context.Context, any)) {
	f.fns[name], f.prio[name] = fn, priority
}

type fakeQueue struct {
	events []string
	data   []string
	err    error
}

func (q *fakeQueue) Enqueue(_ context.Context, event string, data any, _ time.Time) (int, error) {
	b, _ := json.Marshal(data)
	q.events, q.data = append(q.events, event), append(q.data, string(b))
	return 1, q.err
}

func TestRegisterMapsEveryHookToTheContractPayload(t *testing.T) {
	reg := &fakeRegistrar{fns: map[string]func(context.Context, any){}, prio: map[string]int{}}
	q := &fakeQueue{}
	Register(reg, q, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ent := sdk.EntityEvent{ID: "e1", SchemaSlug: "post", Slug: "hello", Locale: "en", Name: "Hello", Status: "published", Version: 7}
	for _, tc := range []struct {
		hook    string
		payload any
		want    string
	}{
		{"entity.created", ent, `{"entity_id":"e1","entity_name":"Hello","schema_slug":"post","locale":"en"}`},
		{"entity.updated", ent, `{"entity_id":"e1","entity_name":"Hello","version":7,"locale":"en"}`},
		{"entity.published", ent, `{"entity_id":"e1","entity_name":"Hello","locale":"en"}`},
		{"entity.deleted", ent, `{"entity_id":"e1","entity_name":"Hello","schema_slug":"post","locale":"en"}`},
		{"import.completed", sdk.ImportEvent{Source: "wordpress", Created: 3, Updated: 2, Failed: 1}, `{"created":3,"failed":1,"source":"wordpress","updated":2}`},
		{"form.submitted", sdk.FormSubmittedEvent{FormID: "f1", FormSlug: "contact", SubmissionID: "s1", Fields: map[string]any{"email": "x@example.com"}, SourcePath: "/contact"},
			`{"form_id":"f1","form_slug":"contact","submission_id":"s1"}`},
		{"form.submitted", sdk.FormSubmittedEvent{FormID: "f1", FormSlug: "contact", SubmissionID: "s1", Fields: map[string]any{"email": "x@example.com"},
			Attribution: &sdk.FormAttribution{First: &sdk.FormTouch{Channel: "direct", Landing: "/"}, Last: &sdk.FormTouch{Channel: "email", Source: "newsletter", Medium: "email"}}},
			`{"form_id":"f1","form_slug":"contact","submission_id":"s1","attribution":{"first":{"channel":"direct","landing":"/"},"last":{"channel":"email","source":"newsletter","medium":"email"}}}`},
	} {
		fn := reg.fns[tc.hook]
		if fn == nil {
			t.Fatalf("%s: no listener registered", tc.hook)
		}
		if reg.prio[tc.hook] != 1000 {
			t.Errorf("%s priority = %d", tc.hook, reg.prio[tc.hook])
		}
		*q = fakeQueue{}
		fn(context.Background(), tc.payload)
		if len(q.events) != 1 || q.events[0] != tc.hook || q.data[0] != tc.want {
			t.Errorf("%s: enqueued %v %v, want %s", tc.hook, q.events, q.data, tc.want)
		}
	}
	if len(reg.fns) != 6 {
		t.Errorf("registered %d hooks, want 6", len(reg.fns))
	}
}

func TestFormPayloadNeverCarriesFields(t *testing.T) {
	data, ok := mapPayload("form.submitted", sdk.FormSubmittedEvent{
		FormID: "f", FormSlug: "s", SubmissionID: "x", Fields: map[string]any{"email": "x@example.com"}, SourcePath: "/p"})
	if !ok {
		t.Fatal("not mapped")
	}
	b, _ := json.Marshal(data)
	for _, bad := range []string{"email", "x@example.com", "fields", "source_path", "submitted_at"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("payload %s contains %q", b, bad)
		}
	}
}

func TestUnexpectedPayloadIsIgnored(t *testing.T) {
	reg := &fakeRegistrar{fns: map[string]func(context.Context, any){}, prio: map[string]int{}}
	q := &fakeQueue{}
	Register(reg, q, slog.New(slog.NewTextHandler(io.Discard, nil)))
	reg.fns["entity.created"](context.Background(), "not an event")
	reg.fns["entity.created"](context.Background(), sdk.ImportEvent{})
	if len(q.events) != 0 {
		t.Fatalf("enqueued %v", q.events)
	}
}
