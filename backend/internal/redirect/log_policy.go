package redirect

import "strings"

// Log404Policy decides which missing addresses are written to the 404 log.
// The zero value logs everything.
type Log404Policy struct {
	// Disabled turns 404 logging off.
	Disabled bool
	// Ignore lists patterns for paths that are not logged. "*" matches any
	// run of characters (including "/"); everything else is literal and
	// case-sensitive. The query string is not part of the matched path.
	Ignore []string
}

// SetLogPolicy installs the function Log404Async consults before it queues an
// entry. It runs on the request path, so it must be cheap and bounded (a
// cached or deadlined read, never an open-ended database call).
// Without a policy every 404 is logged.
func (s *Service) SetLogPolicy(fn func() Log404Policy) {
	if fn == nil {
		s.logPolicy.Store(nil)
		return
	}
	s.logPolicy.Store(&fn)
}

// skipLog reports whether the current policy says not to log path.
func (s *Service) skipLog(path string) bool {
	fn := s.logPolicy.Load()
	if fn == nil {
		return false
	}
	p := (*fn)()
	return p.Disabled || matchIgnore(p.Ignore, path)
}

// maxMatchPath is the longest path the ignore patterns are matched against.
// Matching costs len(pattern)*len(path), so longer (attacker-chosen) paths are
// not matched and are logged as if no pattern applied.
const maxMatchPath = 2048

// matchIgnore reports whether path, without its query string, matches any
// pattern.
func matchIgnore(patterns []string, path string) bool {
	if len(patterns) == 0 {
		return false
	}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if len(path) > maxMatchPath {
		return false
	}
	for _, p := range patterns {
		if wildcardMatch(p, path) {
			return true
		}
	}
	return false
}

// wildcardMatch matches s against pattern with "*" as any run of characters.
// It is iterative and remembers only the last star, so the cost is bounded by
// len(pattern)*len(s).
func wildcardMatch(pattern, s string) bool {
	pi, si := 0, 0
	star, mark := -1, 0
	for si < len(s) {
		switch {
		case pi < len(pattern) && pattern[pi] == '*':
			star, mark = pi, si
			pi++
		case pi < len(pattern) && pattern[pi] == s[si]:
			pi++
			si++
		case star >= 0:
			mark++
			si = mark
			pi = star + 1
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}
