package commentsubmit

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/comment"
	"hatamoto.dev/core/internal/hooks"
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
		p.Score += 2
		p.Reasons = append(p.Reasons, "sus")
		return p, nil
	}}
	in := comment.CheckInput{Body: "hi", LinkDomains: []string{"a.test"}, BodyHash: "h", IPHash: "ip", NetHash: "net"}
	s, r, err := HookChecker{Engine: f}.Check(context.Background(), in)
	if err != nil || s != 2 || len(r) != 1 || r[0] != "sus" {
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
