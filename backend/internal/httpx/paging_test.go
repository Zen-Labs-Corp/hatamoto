package httpx

import (
	"net/url"
	"testing"
)

func TestPaging(t *testing.T) {
	t.Parallel()
	size := func(raw string) (int, error) {
		q := url.Values{}
		if raw != "" {
			q.Set("limit", raw)
		}
		p := NewPaging(q, 0)
		n := p.Size("limit", "per_page", 50)
		return n, p.Err()
	}
	for _, tc := range []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{"", 50, false}, {"1", 1, false}, {"100", 100, false},
		{"200", 100, false}, // over the cap: the cap, not the default
		{"0", 0, true}, {"-3", 0, true}, {"abc", 0, true}, {"1.5", 0, true}, {" 5", 0, true},
	} {
		if got, err := size(tc.raw); (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("limit=%q: %d, %v; want %d, error %v", tc.raw, got, err, tc.want, tc.wantErr)
		}
	}
	p := NewPaging(url.Values{"per_page": {"10"}}, 0)
	if p.Size("limit", "per_page", 20); p.Err() == nil || p.Err().Error() != "unknown parameter per_page; use limit" {
		t.Errorf("other spelling: %v", p.Err())
	}
	p = NewPaging(url.Values{"per_page": {"500"}}, 200)
	if n := p.Size("per_page", "", 50); n != 200 || p.Err() != nil {
		t.Errorf("own cap: %d, %v", n, p.Err())
	}
	for raw, want := range map[string]int{"": 0, "0": 0, "7": 7} {
		p := NewPaging(url.Values{"offset": {raw}}, 0)
		if n := p.Offset("offset"); n != want || p.Err() != nil {
			t.Errorf("offset=%q: %d, %v", raw, n, p.Err())
		}
	}
	if p := NewPaging(url.Values{"offset": {"-1"}}, 0); p.Offset("offset") != 0 || p.Err() == nil {
		t.Error("offset=-1 accepted")
	}
	for raw, ok := range map[string]bool{"": true, "1": true, "10000": true, "0": false, "x": false, "10001": false} {
		p := NewPaging(url.Values{"page": {raw}}, 0)
		n := p.Page("page", 10000)
		if (p.Err() == nil) != ok || (ok && n < 1) {
			t.Errorf("page=%q: %d, %v", raw, n, p.Err())
		}
	}
	// The first error wins and later reads return zero.
	p = NewPaging(url.Values{"page": {"x"}, "limit": {"5"}}, 0)
	if p.Page("page", 0); p.Size("limit", "", 20) != 0 || p.Err() == nil {
		t.Error("a later read hid the first error")
	}
}
