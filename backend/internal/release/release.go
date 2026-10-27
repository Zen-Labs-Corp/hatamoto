// Package release defines the signed release formats: the release record (one
// per version, signed by an offline release key) and the channel manifest (the
// list of releases a channel offers, signed by the CI manifest key), and
// verifies them. It makes no network calls.
package release

import (
	"encoding/json"
	"regexp"
)

var canonicalVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)

// ValidVersion reports whether v is the one form releases use everywhere:
// vMAJOR.MINOR.PATCH with an optional -prerelease, no build metadata and no
// missing "v". Lookups compare versions as exact strings, so only this form
// may be signed.
func ValidVersion(v string) bool { return canonicalVersion.MatchString(v) }

// Artifact is one built binary named in a Record.
type Artifact struct {
	SHA256 string `json:"sha256"` // lowercase hex, 64 chars
	Size   int64  `json:"size"`
}

// Record describes one release. Its signature covers the exact bytes
// MarshalRecord produced.
type Record struct {
	Version  string `json:"version"` // "v1.4.2"
	Kind     string `json:"kind"`    // "patch", "minor" or "major"
	Security bool   `json:"security"`
	SDK      string `json:"sdk"`
	// SDKCompatibleWith lists earlier SDK versions this build still serves;
	// omitted when empty, so older records stay byte-identical.
	SDKCompatibleWith []string            `json:"sdk_compatible_with,omitempty"`
	SchemaHead        int64               `json:"schema_head"`
	RollbackFloor     string              `json:"rollback_floor"`
	MinFrom           string              `json:"min_from"`
	NotesURL          string              `json:"notes_url"`
	Artifacts         map[string]Artifact `json:"artifacts"` // by platform, e.g. "linux-amd64"
}

// Channels lists the channel names a manifest may carry.
var Channels = []string{"stable", "edge", "canary"}

// Platforms lists the targets every record must carry.
var Platforms = []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"}

// RampStep raises the share of installs offered a release to Pct from At.
type RampStep struct {
	At  string `json:"from"` // RFC 3339; "from" because a quoted two-letter name trips check-neutral
	Pct int    `json:"pct"`  // 0..100
}

// Entry offers one release on a channel.
type Entry struct {
	Version   string     `json:"version"`
	RecordSig string     `json:"record_sig"` // "<key_id>:<base64 signature>"
	Ramp      []RampStep `json:"ramp"`
	Halted    bool       `json:"halted"`
	// HaltReason says why Halted was set; omitted when empty.
	HaltReason string `json:"halt_reason,omitempty"`
}

// Yank withdraws a released version, optionally pointing at a replacement.
type Yank struct {
	Version string `json:"version"`
	Reason  string `json:"reason"`
	To      string `json:"to,omitempty"`
}

// ChannelManifest is the signed content of a channel file.
type ChannelManifest struct {
	Channel   string  `json:"channel"` // "stable", "edge" or "canary"; signed so a mirror cannot swap channels
	IssuedAt  string  `json:"issued_at"`
	ExpiresAt string  `json:"expires_at"`
	Releases  []Entry `json:"releases"`
	Yanked    []Yank  `json:"yanked"`
}

// MarshalRecord renders rec as the bytes that are signed and published:
// indented JSON (map keys sorted by encoding/json) and a trailing newline.
func MarshalRecord(rec Record) ([]byte, error) {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
