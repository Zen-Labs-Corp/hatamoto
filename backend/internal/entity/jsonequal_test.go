package entity

import (
	"encoding/json"
	"testing"
)

func TestJSONRawEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{`{"a":1,"b":2}`, `{"b":2,"a":1}`, true}, // key order ignored
		{`{"a":1}`, `{ "a" : 1 }`, true},         // whitespace ignored
		{`{"content":"<p>x</p>"}`, `{"content":"<p>x</p>"}`, true},
		{`{"a":1}`, `{"a":2}`, false},            // real change
		{`{"a":1}`, `{"a":1,"b":2}`, false},      // added key
		{`{"n":1000000}`, `{"n":1000000}`, true}, // big int exact
	}
	for _, c := range cases {
		got := jsonRawEqual(json.RawMessage(c.a), json.RawMessage(c.b))
		if got != c.want {
			t.Errorf("jsonRawEqual(%s,%s)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}
