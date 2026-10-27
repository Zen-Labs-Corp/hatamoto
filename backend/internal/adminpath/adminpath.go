// Package adminpath holds the rules for the per-install admin address: its
// format, how strong it is, the list of addresses scanners try, and (in
// generate.go) the random generator. It is a leaf package: pure functions
// plus the Site interface the server fills in. It imports no database,
// server or admin code.
package adminpath

import "context"

// Level says how an address ranks. Higher is better; LevelInvalid and
// LevelReserved are refused outright, LevelWeak needs an explicit confirm.
type Level int

const (
	LevelInvalid  Level = iota // malformed
	LevelReserved              // the site serves something at this address
	LevelWeak                  // guessable
	LevelOK                    // acceptable, random is stronger
	LevelStrong
)

func (l Level) String() string {
	switch l {
	case LevelInvalid:
		return "invalid"
	case LevelReserved:
		return "reserved"
	case LevelWeak:
		return "weak"
	case LevelOK:
		return "ok"
	case LevelStrong:
		return "strong"
	}
	return "unknown"
}

// Reason explains a level. Code is stable ("format", "scanner", "short",
// "dictionary", "site-name", "served", "reserved"); Text is for people.
type Reason struct{ Code, Text string }

// Site tells Assess what the running site serves and what it is called.
// Any method may be unhelpful (false, ""), and Assess accepts a nil Site.
type Site interface {
	// Serves reports whether the first segment of path collides with a URL
	// pattern, entity slug or redirect source, and says what serves it.
	Serves(ctx context.Context, path string) (bool, string)
	// Name is the site name, or "".
	Name() string
	// Domain is the host of the public base URL, or "".
	Domain() string
}

// Result is the outcome of Assess. Path is the normalised form when the
// input was well formed, else the trimmed input.
type Result struct {
	Path    string
	Level   Level
	Reasons []Reason
	Bits    float64
}
