package commentsubmit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/clientkey"
	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/commentfmt"
	"hatamoto.dev/core/internal/hooks"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/spam"
	"hatamoto.dev/sdk"
)

type fakeEngine struct {
	count int
	calls int
	got   any
	apply func(ctx context.Context, v any) (any, error)
}

func (f *fakeEngine) FilterCount(string) int { return f.count }
func (f *fakeEngine) ApplyClosed(ctx context.Context, _ string, v any) (any, error) {
	f.calls++
	f.got = v
	return f.apply(ctx, v)
}

func TestHookCheckerNoFilters(t *testing.T) {
	f := &fakeEngine{}
	s, r, err := HookChecker{Engine: f}.Check(context.Background(), comment.CheckInput{Body: "x"})
	if s != 0 || r != nil || err != nil || f.calls != 0 {
		t.Fatalf("got %d %v %v calls=%d", s, r, err, f.calls)
	}
	if _, _, err := (HookChecker{}).Check(context.Background(), comment.CheckInput{}); err != nil {
		t.Fatal(err)
	}
}

func TestHookCheckerScores(t *testing.T) {
	f := &fakeEngine{count: 1, apply: func(_ context.Context, v any) (any, error) {
		p := v.(CheckPayload)
		p.Score += 40
		p.Reasons = append(p.Reasons, "sus")
		return p, nil
	}}
	in := comment.CheckInput{Body: "hi", LinkDomains: []string{"a.test"}, BodyHash: "h", IPHash: "ip", NetHash: "net"}
	s, r, err := HookChecker{Engine: f}.Check(context.Background(), in)
	if err != nil || s != 4 || len(r) != 1 || r[0] != "sus" {
		t.Fatalf("got %d %v %v", s, r, err)
	}
	want := CheckPayload{Body: "hi", LinkDomains: []string{"a.test"}, BodyHash: "h"}
	if !reflect.DeepEqual(f.got, want) {
		t.Fatalf("payload %+v, want %+v", f.got, want)
	}
}

func TestHookCheckerHolds(t *testing.T) {
	for name, apply := range map[string]func(context.Context, any) (any, error){
		"fail closed": func(context.Context, any) (any, error) { return nil, hooks.ErrFailClosed },
		"wrong type":  func(context.Context, any) (any, error) { return "nope", nil },
	} {
		f := &fakeEngine{count: 1, apply: apply}
		if _, _, err := (HookChecker{Engine: f}).Check(context.Background(), comment.CheckInput{}); err != ErrHookHold {
			t.Errorf("%s: err = %v, want ErrHookHold", name, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeEngine{count: 1, apply: func(_ context.Context, v any) (any, error) { cancel(); return v, nil }}
	if _, _, err := (HookChecker{Engine: f}).Check(ctx, comment.CheckInput{}); err != ErrHookHold {
		t.Errorf("cancelled: err = %v", err)
	}
}

func TestHookCheckerRunCheckersTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	f := &fakeEngine{count: 1, apply: func(_ context.Context, v any) (any, error) { <-release; return v, nil }}
	_, reasons, hold := comment.RunCheckers(context.Background(), []comment.Checker{HookChecker{Engine: f}}, comment.CheckInput{}, 20*time.Millisecond)
	if !hold || len(reasons) == 0 {
		t.Fatalf("hold=%v reasons=%v", hold, reasons)
	}
}

func TestCheckPayloadFields(t *testing.T) {
	typ := reflect.TypeOf(CheckPayload{})
	for i := 0; i < typ.NumField(); i++ {
		n := typ.Field(i).Name
		for _, bad := range []string{"IP", "Net", "Email", "Name"} {
			if strings.HasPrefix(n, bad) {
				t.Errorf("CheckPayload has field %s", n)
			}
		}
	}
}

// Against the real engine: a filter that errors, panics, returns the wrong type
// or runs out of time holds the comment, alone or after a clean one.
func TestHookCheckerRealEngineHolds(t *testing.T) {
	for name, fn := range map[string]func(context.Context, any) (any, error){
		"error":      func(context.Context, any) (any, error) { return nil, errors.New("x") },
		"panic":      func(context.Context, any) (any, error) { panic("boom") },
		"wrong type": func(context.Context, any) (any, error) { return 7, nil },
		"timeout":    func(ctx context.Context, _ any) (any, error) { <-ctx.Done(); return nil, ctx.Err() },
	} {
		e := hooks.New(nil)
		e.AddFilter(HookName, 10, func(_ context.Context, v any) (any, error) { return v, nil })
		e.AddFilter(HookName, 20, fn)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, _, err := HookChecker{Engine: e}.Check(ctx, comment.CheckInput{Body: "x"})
		cancel()
		if err != ErrHookHold {
			t.Errorf("%s: err = %v, want ErrHookHold", name, err)
		}
	}
	// The adapter itself survives an engine that panics.
	f := &fakeEngine{count: 1, apply: func(context.Context, any) (any, error) { panic("boom") }}
	if _, _, err := (HookChecker{Engine: f}).Check(context.Background(), comment.CheckInput{}); err != ErrHookHold {
		t.Errorf("engine panic: err = %v", err)
	}
}

func TestCommentCheckPoints(t *testing.T) {
	for _, c := range []struct{ in, want int }{
		{-5, 0}, {0, 0}, {4, 0}, {5, 1}, {25, 3}, {100, 10}, {1 << 30, 10},
	} {
		if got := commentCheckPoints(c.in); got != c.want {
			t.Errorf("commentCheckPoints(%d) = %d, want %d", c.in, got, c.want)
		}
	}
	if commentCheckPoints(25) < spam.HoldScore || commentCheckPoints(100) != spam.SpamScore {
		t.Error("25 must hold and 100 must be spam alone")
	}
}

func TestHookNameIsSDK(t *testing.T) {
	if HookName != sdk.CommentCheckF.Name {
		t.Fatalf("HookName = %q, want %q", HookName, sdk.CommentCheckF.Name)
	}
}

func TestCheckPayloadWire(t *testing.T) {
	got, err := jsonx.MarshalPinned(CheckPayload{Body: "b", BodyHash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"body":"b","body_hash":"h"}`; string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// ipChecker records the address the pipeline put in the context.
type ipChecker struct {
	ip *string
	in *comment.CheckInput
}

func (c ipChecker) Check(ctx context.Context, in comment.CheckInput) (int, []string, error) {
	*c.ip, *c.in = clientkey.IPFrom(ctx), in
	return 0, nil, nil
}

func TestCommentCheckClientKeys(t *testing.T) {
	var ip string
	var in comment.CheckInput
	h := newHarness(t)
	h.p.Checkers = []comment.Checker{ipChecker{&ip, &in}}
	h.p.Spam.Settings = spamSettings{*h.sp}

	// An edit carries the address in the context; the check input never does.
	res, err := commentfmt.Render("A fine edit.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.p.HoldEdit(context.Background(), EditCheck{
		EntityID: h.req.EntityID, Name: "Ada", Body: "A fine edit.", Rendered: res, BodyChanged: true, IP: "203.0.113.7", Now: t0,
	}); err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.7" {
		t.Errorf("edit: checker saw ip %q", ip)
	}
	ip = ""
	if out := h.submit(t); out.Kind != Posted && out.Kind != CheckEmail {
		t.Fatalf("submit: %v", out.Kind)
	}
	if ip != "203.0.113.7" {
		t.Errorf("new comment: checker saw ip %q", ip)
	}
	if strings.Contains(fmt.Sprintf("%+v", in), "203.0.113.7") {
		t.Errorf("CheckInput carries the address: %+v", in)
	}
}

// The hook checker passes the context on but adds no address or key itself:
// the plugin host derives keys per plugin from the context.
func TestHookCheckerPayloadHasNoAddress(t *testing.T) {
	f := &fakeEngine{count: 1, apply: func(_ context.Context, v any) (any, error) { return v, nil }}
	ctx := clientkey.WithIP(context.Background(), "203.0.113.7")
	if _, _, err := (HookChecker{Engine: f}).Check(ctx, comment.CheckInput{Body: "hi", LinkDomains: []string{"a.test"}, BodyHash: "h", IPHash: "ip", NetHash: "net"}); err != nil {
		t.Fatal(err)
	}
	want := CheckPayload{Body: "hi", LinkDomains: []string{"a.test"}, BodyHash: "h"}
	if !reflect.DeepEqual(f.got, want) {
		t.Fatalf("payload %+v, want %+v", f.got, want)
	}
}
