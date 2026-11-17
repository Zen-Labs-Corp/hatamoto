package config

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/core/internal/jsonx"
)

// DemoPitchFootnote ends every pitch, so a visitor knows the note belongs to
// the demo and not to the product they would install.
const DemoPitchFootnote = "Demo only: this note is not shown on real installs."

// DemoPitch is one note the admin of a public demo shows above a section: a
// short title and a sentence or two of plain text (never HTML).
type DemoPitch struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// Key is the entry that matched (set by DemoPitchFor, not read from the file).
	Key string `json:"-"`
}

// Limits of the pitch file. The text is the operator's, shown to every visitor.
const (
	demoPitchFileMax  = 64 << 10
	demoPitchTitleMax = 80
	demoPitchBodyMax  = 400
	demoPitchCountMax = 100
)

// demoPitchKey is a section ("analytics"), a section and a page
// ("analytics/crawlers"), or "home" for the dashboard.
var demoPitchKey = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(/[a-z0-9][a-z0-9-]*)?$`)

// LoadDemoPitches reads HATAMOTO_DEMO_PITCH_FILE: a JSON object of key ->
// {"title", "body"}. Any problem is an error, so a broken file stops the
// server at start instead of silently showing nothing.
func LoadDemoPitches(path string) (map[string]DemoPitch, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("HATAMOTO_DEMO_PITCH_FILE: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, demoPitchFileMax+1))
	if err != nil {
		return nil, fmt.Errorf("HATAMOTO_DEMO_PITCH_FILE: %w", err)
	}
	if len(raw) > demoPitchFileMax {
		return nil, fmt.Errorf("HATAMOTO_DEMO_PITCH_FILE: larger than %d KB", demoPitchFileMax>>10)
	}
	// Strict: no unknown fields, no duplicate keys, no trailing data.
	var m map[string]DemoPitch
	if err := jsonx.Unmarshal(raw, &m, jsonx.StrictNoUnknown); err != nil {
		return nil, fmt.Errorf("HATAMOTO_DEMO_PITCH_FILE: %w", err)
	}
	if len(m) > demoPitchCountMax {
		return nil, fmt.Errorf("HATAMOTO_DEMO_PITCH_FILE: more than %d entries", demoPitchCountMax)
	}
	for k, p := range m {
		if !demoPitchKey.MatchString(k) {
			return nil, fmt.Errorf("HATAMOTO_DEMO_PITCH_FILE: key %q: use a section (\"analytics\"), a section/page (\"analytics/crawlers\") or \"home\"", k)
		}
		p.Title, p.Body = strings.TrimSpace(p.Title), strings.TrimSpace(p.Body)
		switch {
		case p.Body == "":
			return nil, fmt.Errorf("HATAMOTO_DEMO_PITCH_FILE: %q has no body", k)
		case utf8.RuneCountInString(p.Title) > demoPitchTitleMax:
			return nil, fmt.Errorf("HATAMOTO_DEMO_PITCH_FILE: %q: title longer than %d characters", k, demoPitchTitleMax)
		case utf8.RuneCountInString(p.Body) > demoPitchBodyMax:
			return nil, fmt.Errorf("HATAMOTO_DEMO_PITCH_FILE: %q: body longer than %d characters", k, demoPitchBodyMax)
		}
		m[k] = p
	}
	return m, nil
}

// DemoPitchFor returns the pitch for an admin path relative to the admin
// address ("/analytics/crawlers", "/" for the dashboard): the section/page
// key first, then the section, "home" for the dashboard. Nothing outside the
// demo or without a pitch file.
func (d DemoConfig) DemoPitchFor(rel string) (DemoPitch, bool) {
	if !d.Enabled || len(d.Pitches) == 0 {
		return DemoPitch{}, false
	}
	parts := strings.Split(strings.Trim(rel, "/"), "/")
	keys := []string{parts[0]}
	if parts[0] == "" {
		keys = []string{"home"}
	} else if len(parts) >= 2 {
		keys = []string{parts[0] + "/" + parts[1], parts[0]}
	}
	for _, k := range keys {
		if p, ok := d.Pitches[k]; ok {
			p.Key = k
			return p, true
		}
	}
	return DemoPitch{}, false
}
