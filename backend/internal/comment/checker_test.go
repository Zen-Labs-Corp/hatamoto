package comment

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeChecker func(ctx context.Context) (int, []string, error)

func (f fakeChecker) Check(ctx context.Context, _ CheckInput) (int, []string, error) { return f(ctx) }

func TestRunCheckers(t *testing.T) {
	t.Parallel()
	ok := func(n int, r string) Checker {
		return fakeChecker(func(context.Context) (int, []string, error) { return n, []string{r}, nil })
	}
	t.Run("sums", func(t *testing.T) {
		t.Parallel()
		s, r, hold := RunCheckers(context.Background(), []Checker{ok(2, "a"), ok(3, "b")}, CheckInput{}, time.Second)
		if s != 5 || hold || len(r) != 2 {
			t.Errorf("got %d %v %v", s, r, hold)
		}
	})
	t.Run("error holds", func(t *testing.T) {
		t.Parallel()
		bad := fakeChecker(func(context.Context) (int, []string, error) { return 0, nil, errors.New("x") })
		s, r, hold := RunCheckers(context.Background(), []Checker{ok(2, "a"), bad}, CheckInput{}, time.Second)
		if !hold || s != 2 || r[len(r)-1] != ReasonCheckFailed {
			t.Errorf("got %d %v %v", s, r, hold)
		}
	})
	t.Run("panic holds", func(t *testing.T) {
		t.Parallel()
		bad := fakeChecker(func(context.Context) (int, []string, error) { panic("boom") })
		if _, _, hold := RunCheckers(context.Background(), []Checker{bad}, CheckInput{}, time.Second); !hold {
			t.Error("panic should hold")
		}
	})
	t.Run("timeout holds even when ctx is ignored", func(t *testing.T) {
		t.Parallel()
		slow := fakeChecker(func(context.Context) (int, []string, error) { time.Sleep(time.Second); return 9, nil, nil })
		start := time.Now()
		_, r, hold := RunCheckers(context.Background(), []Checker{slow}, CheckInput{}, 20*time.Millisecond)
		if !hold || r[len(r)-1] != ReasonCheckTimedOut {
			t.Errorf("got %v %v", r, hold)
		}
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Errorf("took %v", d)
		}
	})
}

func TestCheckInputHasNoIPOrEmail(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(CheckInput{})
	for i := 0; i < typ.NumField(); i++ {
		n := strings.ToLower(typ.Field(i).Name)
		if n == "ip" || strings.Contains(n, "email") || strings.Contains(n, "addr") {
			t.Errorf("CheckInput.%s must not exist", typ.Field(i).Name)
		}
	}
}
