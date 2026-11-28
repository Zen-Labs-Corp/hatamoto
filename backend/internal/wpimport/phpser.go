package wpimport

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	phpMaxLen   = 1 << 20 // longest serialized value read, in bytes
	phpMaxDepth = 32      // deepest array nesting read
)

// phpArray is a PHP array: keys keep their source order, values are string,
// int64, float64, bool, nil or *phpArray.
type phpArray struct {
	Keys []string
	Vals map[string]any
}

// phpUnserialize reads the scalar and array subset of PHP serialize() that
// WordPress stores in meta values. Objects are refused. The input is
// untrusted: it is bounded in size and depth and a malformed value is an
// error, never a panic.
func phpUnserialize(s string) (any, error) {
	if len(s) > phpMaxLen {
		return nil, errors.New("php value too large")
	}
	p := &phpParser{s: s}
	v, err := p.value(0)
	if err != nil {
		return nil, fmt.Errorf("php unserialize at byte %d: %w", p.i, err)
	}
	return v, nil
}

type phpParser struct {
	s string
	i int
}

func (p *phpParser) expect(c byte) error {
	if p.i >= len(p.s) || p.s[p.i] != c {
		return fmt.Errorf("expected %q", c)
	}
	p.i++
	return nil
}

// until returns the text up to the next terminator and skips it.
func (p *phpParser) until(c byte) (string, error) {
	j := strings.IndexByte(p.s[p.i:], c)
	if j < 0 {
		return "", errors.New("unexpected end")
	}
	out := p.s[p.i : p.i+j]
	p.i += j + 1
	return out, nil
}

func (p *phpParser) count() (int, error) {
	t, err := p.until(':')
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(t)
	if err != nil || n < 0 || n > len(p.s) {
		return 0, errors.New("bad length")
	}
	return n, nil
}

func (p *phpParser) str() (string, error) {
	n, err := p.count()
	if err != nil {
		return "", err
	}
	if err = p.expect('"'); err != nil {
		return "", err
	}
	if p.i+n > len(p.s) {
		return "", errors.New("string longer than input")
	}
	out := p.s[p.i : p.i+n]
	p.i += n
	if err = p.expect('"'); err != nil {
		return "", err
	}
	return out, nil
}

func (p *phpParser) value(depth int) (any, error) {
	if depth > phpMaxDepth {
		return nil, errors.New("nested too deep")
	}
	if p.i+1 >= len(p.s) {
		return nil, errors.New("unexpected end")
	}
	typ := p.s[p.i]
	p.i++
	if typ == 'N' {
		return nil, p.expect(';')
	}
	if err := p.expect(':'); err != nil {
		return nil, err
	}
	switch typ {
	case 'i':
		t, err := p.until(';')
		if err != nil {
			return nil, err
		}
		return strconv.ParseInt(t, 10, 64)
	case 'd':
		t, err := p.until(';')
		if err != nil {
			return nil, err
		}
		return strconv.ParseFloat(t, 64)
	case 'b':
		t, err := p.until(';')
		if err != nil {
			return nil, err
		}
		return t == "1", nil
	case 's':
		v, err := p.str()
		if err != nil {
			return nil, err
		}
		return v, p.expect(';')
	case 'a':
		return p.array(depth)
	}
	return nil, fmt.Errorf("unsupported type %q", typ)
}

func (p *phpParser) array(depth int) (*phpArray, error) {
	n, err := p.count()
	if err != nil {
		return nil, err
	}
	if err = p.expect('{'); err != nil {
		return nil, err
	}
	a := &phpArray{Vals: map[string]any{}}
	for range n {
		k, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		var key string
		switch kv := k.(type) {
		case string:
			key = kv
		case int64:
			key = strconv.FormatInt(kv, 10)
		default:
			return nil, errors.New("bad array key")
		}
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if _, dup := a.Vals[key]; !dup {
			a.Keys = append(a.Keys, key)
		}
		a.Vals[key] = v
	}
	return a, p.expect('}')
}

// phpStrings returns the scalar values of a serialized PHP array as strings,
// in order. A value that is not serialized is one plain string; "" gives nil.
func phpStrings(s string) []string {
	if s == "" {
		return nil
	}
	if !strings.HasPrefix(s, "a:") {
		return []string{s}
	}
	v, err := phpUnserialize(s)
	a, ok := v.(*phpArray)
	if err != nil || !ok {
		return []string{s}
	}
	var out []string
	for _, k := range a.Keys {
		if t, ok := phpScalar(a.Vals[k]); ok {
			out = append(out, t)
		}
	}
	return out
}

// phpScalar renders a scalar PHP value as text.
func phpScalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case int64:
		return strconv.FormatInt(t, 10), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		if t {
			return "1", true
		}
		return "0", true
	}
	return "", false
}

// phpGet returns the value of key in a PHP array, or nil.
func phpGet(v any, key string) any {
	if a, ok := v.(*phpArray); ok {
		return a.Vals[key]
	}
	return nil
}

// phpText returns key of a PHP array as text, or "".
func phpText(v any, key string) string {
	t, _ := phpScalar(phpGet(v, key))
	return t
}
