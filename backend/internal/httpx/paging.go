package httpx

import (
	"fmt"
	"net/url"
	"strconv"
)

// MaxPageSize is the default cap on the page size of a REST list.
const MaxPageSize = 100

// Paging reads the paging parameters of one REST list request. A parameter
// the list cannot honour is refused (Err, answered with 400) instead of
// falling back to a default without a word; a page size over the cap is
// clamped to it, and the list reports the size it used. The first error wins.
type Paging struct {
	q   url.Values
	max int
	err error
}

// NewPaging reads q with the page size capped at max (MaxPageSize when 0).
func NewPaging(q url.Values, max int) *Paging {
	if max <= 0 {
		max = MaxPageSize
	}
	return &Paging{q: q, max: max}
}

// Err is the first refused parameter, or nil.
func (p *Paging) Err() error { return p.err }

// Size reads the page size under name: absent gives def, a whole number of 1
// or more gives itself clamped to the cap. other is the spelling this list
// does not take (limit or per_page); it is refused with a hint, never ignored.
func (p *Paging) Size(name, other string, def int) int {
	if p.err != nil {
		return 0
	}
	if other != "" && p.q.Has(other) {
		p.err = fmt.Errorf("unknown parameter %s; use %s", other, name)
		return 0
	}
	n, ok := p.int(name, 1)
	if !ok {
		p.err = fmt.Errorf("invalid %s: use a whole number from 1 to %d", name, p.max)
		return 0
	}
	if n == 0 {
		return def
	}
	return min(n, p.max)
}

// Offset reads a 0-based offset: absent gives 0.
func (p *Paging) Offset(name string) int {
	if p.err != nil {
		return 0
	}
	n, ok := p.int(name, 0)
	if !ok {
		p.err = fmt.Errorf("invalid %s: use a whole number of 0 or more", name)
		return 0
	}
	return n
}

// Page reads a 1-based page number: absent gives 1. max > 0 refuses a larger
// page (a deep page only forces a scan that is thrown away).
func (p *Paging) Page(name string, max int) int {
	if p.err != nil {
		return 0
	}
	n, ok := p.int(name, 1)
	switch {
	case !ok:
		p.err = fmt.Errorf("invalid %s: use a whole number of 1 or more", name)
		return 0
	case max > 0 && n > max:
		p.err = fmt.Errorf("%s must not exceed %d", name, max)
		return 0
	case n == 0:
		return 1
	}
	return n
}

// int reads name as a whole number of at least lo. Absent is (0, true) when
// lo is 1; with lo 0 an absent and a "0" value both give 0.
func (p *Paging) int(name string, lo int) (int, bool) {
	raw := p.q.Get(name)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo {
		return 0, false
	}
	return n, true
}
