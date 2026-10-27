// Package testutil holds the small assertion helpers shared by tests, so they
// stay on the stdlib testing package. Plain functions (Equal, NoError, ...)
// mark the test failed and continue; the Require-prefixed forms stop it.
package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

// failf reports a failure, stopping the test when fatal is set.
func failf(t testing.TB, fatal bool, msg []any, format string, args ...any) {
	t.Helper()
	text := fmt.Sprintf(format, args...)
	if len(msg) > 0 {
		if f, ok := msg[0].(string); ok {
			text += ": " + fmt.Sprintf(f, msg[1:]...)
		} else {
			text += ": " + fmt.Sprint(msg...)
		}
	}
	if fatal {
		t.Fatal(text)
	}
	t.Error(text)
}

func equal(a, b any) bool {
	if ab, ok := a.([]byte); ok {
		if bb, ok := b.([]byte); ok {
			return bytes.Equal(ab, bb)
		}
	}
	return reflect.DeepEqual(a, b)
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	}
	return false
}

func length(v any) (int, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Array, reflect.Chan, reflect.Map, reflect.Slice, reflect.String:
		return rv.Len(), true
	}
	return 0, false
}

func isEmpty(v any) bool {
	if isNil(v) {
		return true
	}
	if n, ok := length(v); ok {
		return n == 0
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		return isEmpty(rv.Elem().Interface())
	}
	return rv.IsZero()
}

// contains reports whether container (string, slice, array or map keys) holds elem.
func contains(container, elem any) (found, ok bool) {
	rv := reflect.ValueOf(container)
	switch rv.Kind() {
	case reflect.String:
		s, isStr := elem.(string)
		if !isStr {
			return false, false
		}
		return strings.Contains(rv.String(), s), true
	case reflect.Map:
		for _, k := range rv.MapKeys() {
			if equal(k.Interface(), elem) {
				return true, true
			}
		}
		return false, true
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			if equal(rv.Index(i).Interface(), elem) {
				return true, true
			}
		}
		return false, true
	}
	return false, false
}

func equalImpl(t testing.TB, fatal bool, want, got any, msg []any) {
	t.Helper()
	if !equal(want, got) {
		failf(t, fatal, msg, "not equal:\n  want: %#v\n  got:  %#v", want, got)
	}
}

func notEqualImpl(t testing.TB, fatal bool, a, b any, msg []any) {
	t.Helper()
	if equal(a, b) {
		failf(t, fatal, msg, "values should differ, both are %#v", a)
	}
}

func errorImpl(t testing.TB, fatal bool, err error, msg []any) {
	t.Helper()
	if err == nil {
		failf(t, fatal, msg, "expected an error, got nil")
	}
}

func noErrorImpl(t testing.TB, fatal bool, err error, msg []any) {
	t.Helper()
	if err != nil {
		failf(t, fatal, msg, "unexpected error: %v", err)
	}
}

func errorIsImpl(t testing.TB, fatal bool, err, target error, msg []any) {
	t.Helper()
	if !errors.Is(err, target) {
		failf(t, fatal, msg, "error %v does not wrap %v", err, target)
	}
}

func lenImpl(t testing.TB, fatal bool, v any, want int, msg []any) {
	t.Helper()
	n, ok := length(v)
	if !ok {
		failf(t, fatal, msg, "%#v has no length", v)
		return
	}
	if n != want {
		failf(t, fatal, msg, "length is %d, want %d (%#v)", n, want, v)
	}
}

func containsImpl(t testing.TB, fatal bool, container, elem any, want bool, msg []any) {
	t.Helper()
	found, ok := contains(container, elem)
	if !ok {
		failf(t, fatal, msg, "cannot search %#v for %#v", container, elem)
		return
	}
	if found != want {
		if want {
			failf(t, fatal, msg, "%#v does not contain %#v", container, elem)
		} else {
			failf(t, fatal, msg, "%#v should not contain %#v", container, elem)
		}
	}
}

func jsonEqualImpl(t testing.TB, fatal bool, want, got string, msg []any) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		failf(t, fatal, msg, "expected value is not JSON: %v", err)
		return
	}
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		failf(t, fatal, msg, "actual value is not JSON: %v", err)
		return
	}
	if !reflect.DeepEqual(w, g) {
		failf(t, fatal, msg, "JSON differs:\n  want: %s\n  got:  %s", want, got)
	}
}

func boolImpl(t testing.TB, fatal bool, got, want bool, msg []any) {
	t.Helper()
	if got != want {
		failf(t, fatal, msg, "expected %v", want)
	}
}

// Equal fails the test unless want and got are deeply equal.
func Equal(t testing.TB, want, got any, msg ...any) { t.Helper(); equalImpl(t, false, want, got, msg) }

// RequireEqual is Equal, stopping the test on failure.
func RequireEqual(t testing.TB, want, got any, msg ...any) {
	t.Helper()
	equalImpl(t, true, want, got, msg)
}

// NotEqual fails the test when a and b are deeply equal.
func NotEqual(t testing.TB, a, b any, msg ...any) { t.Helper(); notEqualImpl(t, false, a, b, msg) }

// EqualError fails the test unless err is non-nil with exactly the given message.
func EqualError(t testing.TB, err error, want string, msg ...any) {
	t.Helper()
	if err == nil {
		failf(t, false, msg, "expected error %q, got nil", want)
		return
	}
	if err.Error() != want {
		failf(t, false, msg, "error is %q, want %q", err.Error(), want)
	}
}

// Error fails the test when err is nil.
func Error(t testing.TB, err error, msg ...any) { t.Helper(); errorImpl(t, false, err, msg) }

// RequireError is Error, stopping the test on failure.
func RequireError(t testing.TB, err error, msg ...any) { t.Helper(); errorImpl(t, true, err, msg) }

// NoError fails the test when err is non-nil.
func NoError(t testing.TB, err error, msg ...any) { t.Helper(); noErrorImpl(t, false, err, msg) }

// RequireNoError is NoError, stopping the test on failure.
func RequireNoError(t testing.TB, err error, msg ...any) {
	t.Helper()
	noErrorImpl(t, true, err, msg)
}

// ErrorIs fails the test unless err wraps target.
func ErrorIs(t testing.TB, err, target error, msg ...any) {
	t.Helper()
	errorIsImpl(t, false, err, target, msg)
}

// RequireErrorIs is ErrorIs, stopping the test on failure.
func RequireErrorIs(t testing.TB, err, target error, msg ...any) {
	t.Helper()
	errorIsImpl(t, true, err, target, msg)
}

// NotErrorIs fails the test when err wraps target.
func NotErrorIs(t testing.TB, err, target error, msg ...any) {
	t.Helper()
	if errors.Is(err, target) {
		failf(t, false, msg, "error %v should not wrap %v", err, target)
	}
}

// Len fails the test unless v (string, slice, array, map or chan) has length want.
func Len(t testing.TB, v any, want int, msg ...any) { t.Helper(); lenImpl(t, false, v, want, msg) }

// RequireLen is Len, stopping the test on failure.
func RequireLen(t testing.TB, v any, want int, msg ...any) {
	t.Helper()
	lenImpl(t, true, v, want, msg)
}

// Contains fails the test unless the string, slice, array or map holds elem.
func Contains(t testing.TB, container, elem any, msg ...any) {
	t.Helper()
	containsImpl(t, false, container, elem, true, msg)
}

// NotContains fails the test when the string, slice, array or map holds elem.
func NotContains(t testing.TB, container, elem any, msg ...any) {
	t.Helper()
	containsImpl(t, false, container, elem, false, msg)
}

// JSONEqual fails the test unless both strings are JSON documents with equal content.
func JSONEqual(t testing.TB, want, got string, msg ...any) {
	t.Helper()
	jsonEqualImpl(t, false, want, got, msg)
}

// RequireJSONEqual is JSONEqual, stopping the test on failure.
func RequireJSONEqual(t testing.TB, want, got string, msg ...any) {
	t.Helper()
	jsonEqualImpl(t, true, want, got, msg)
}

// True fails the test unless v is true.
func True(t testing.TB, v bool, msg ...any) { t.Helper(); boolImpl(t, false, v, true, msg) }

// False fails the test unless v is false.
func False(t testing.TB, v bool, msg ...any) { t.Helper(); boolImpl(t, false, v, false, msg) }

// Nil fails the test unless v is nil (including typed nil pointers, slices, maps).
func Nil(t testing.TB, v any, msg ...any) {
	t.Helper()
	if !isNil(v) {
		failf(t, false, msg, "expected nil, got %#v", v)
	}
}

// NotNil fails the test when v is nil.
func NotNil(t testing.TB, v any, msg ...any) {
	t.Helper()
	if isNil(v) {
		failf(t, false, msg, "expected a non-nil value")
	}
}

// RequireNotNil is NotNil, stopping the test on failure.
func RequireNotNil(t testing.TB, v any, msg ...any) {
	t.Helper()
	if isNil(v) {
		failf(t, true, msg, "expected a non-nil value")
	}
}

// Empty fails the test unless v is nil, zero, or an empty string, slice, map or array.
func Empty(t testing.TB, v any, msg ...any) {
	t.Helper()
	if !isEmpty(v) {
		failf(t, false, msg, "expected empty, got %#v", v)
	}
}

// NotEmpty fails the test when v is empty.
func NotEmpty(t testing.TB, v any, msg ...any) {
	t.Helper()
	if isEmpty(v) {
		failf(t, false, msg, "expected a non-empty value")
	}
}

// LessOrEqual fails the test unless a <= b.
func LessOrEqual[T int | int64 | float64 | string](t testing.TB, a, b T, msg ...any) {
	t.Helper()
	if a > b {
		failf(t, false, msg, "%v should be <= %v", a, b)
	}
}

// QueryCounter is a pgx.QueryTracer that records the statements a pool runs.
// Set it as ConnConfig.Tracer before the pool is created, then compare Count
// before and after the code under test (Since lists what ran in between).
type QueryCounter struct {
	mu  sync.Mutex
	sql []string
}

// TraceQueryStart records one statement.
func (c *QueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	c.sql = append(c.sql, d.SQL)
	c.mu.Unlock()
	return ctx
}

// TraceQueryEnd is part of pgx.QueryTracer.
func (c *QueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// Count is the number of statements started so far.
func (c *QueryCounter) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sql)
}

// Since returns the statements started after the first n (n from Count).
func (c *QueryCounter) Since(n int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sql[n:]...)
}
