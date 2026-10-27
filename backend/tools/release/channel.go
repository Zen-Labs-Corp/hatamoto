package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

// manifestLifetime is how long a resigned channel manifest stays valid. The
// verifier refuses anything over release.MaxManifestLifetime (15 days).
const manifestLifetime = 14 * 24 * time.Hour

// defaultChannelDir is where the unsigned channel sources live, as seen from
// backend/ where the tool is run.
const defaultChannelDir = "../releases/channels"

var channelNames = []string{"stable", "edge", "canary"}

// cmdChannel edits the unsigned channel sources and signs them for publishing.
func cmdChannel(args []string, stdout, stderr io.Writer, e env) error {
	const usageText = `usage: channel add CHAN VERSION --record F --sig F --ramp SPEC [--dir D]
       channel ramp CHAN VERSION --ramp SPEC [--dir D]
       channel halt CHAN VERSION [--reason TEXT] [--dir D]
       channel unhalt CHAN VERSION [--dir D]
       channel yank CHAN VERSION --reason TEXT [--to VERSION] [--dir D]
       channel resign --channel CHAN --out DIR --key F [--in D]`
	if len(args) == 0 {
		return errors.New(usageText)
	}
	sub, args := args[0], args[1:]
	var dir, recordFile, sigFile, rampSpec, reason, to, in, out, keyFile, chanFlag string
	fs := newFlags("channel "+sub, stderr)
	fs.StringVar(&dir, "dir", defaultChannelDir, "directory holding the channel source files")
	fs.StringVar(&recordFile, "record", "", "release record (add)")
	fs.StringVar(&sigFile, "sig", "", "detached record signature (add)")
	fs.StringVar(&rampSpec, "ramp", "", "rollout steps such as now:5,+2d:25,+4d:100")
	fs.StringVar(&reason, "reason", "", "why the release is yanked or halted")
	fs.StringVar(&to, "to", "", "version to move to instead (yank)")
	fs.StringVar(&in, "in", defaultChannelDir, "directory holding the channel sources (resign)")
	fs.StringVar(&out, "out", "", "directory for the signed manifest (resign)")
	fs.StringVar(&keyFile, "key", "", "manifest key file (resign)")
	fs.StringVar(&chanFlag, "channel", "", "channel to sign (resign)")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if sub == "resign" {
		if len(pos) != 0 || chanFlag == "" || out == "" || keyFile == "" {
			return errors.New("usage: channel resign --channel CHAN --out DIR --key F [--in D]")
		}
		return resignChannel(in, chanFlag, out, keyFile, stdout, e)
	}
	if len(pos) != 2 {
		return errors.New(usageText)
	}
	name, version := pos[0], pos[1]
	if !release.ValidVersion(version) {
		return fmt.Errorf("%q is not a version like v1.2.3 (no build metadata, no missing v)", version)
	}
	path, err := channelPath(dir, name)
	if err != nil {
		return err
	}
	m, err := loadChannel(path, name)
	if err != nil {
		return err
	}
	switch sub {
	case "add":
		if recordFile == "" || sigFile == "" || rampSpec == "" {
			return errors.New("channel add needs --record, --sig and --ramp")
		}
		err = addRelease(&m, version, recordFile, sigFile, rampSpec, e)
	case "ramp":
		var ramp []release.RampStep
		if ramp, err = parseRamp(rampSpec, e.now()); err == nil {
			err = withEntry(&m, version, func(en *release.Entry) { en.Ramp = ramp })
		}
	case "halt", "unhalt":
		err = withEntry(&m, version, func(en *release.Entry) {
			en.Halted = sub == "halt"
			en.HaltReason = ""
			if en.Halted {
				en.HaltReason = strings.TrimSpace(reason)
			}
		})
	case "yank":
		err = yankRelease(&m, version, reason, to)
	default:
		return fmt.Errorf("unknown channel command %q\n%s", sub, usageText)
	}
	if err != nil {
		return err
	}
	if err := saveChannel(path, m); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s %s on %s\n", sub, version, name)
	return nil
}

func channelPath(dir, name string) (string, error) {
	for _, n := range channelNames {
		if n == name {
			return filepath.Join(dir, name+".json"), nil
		}
	}
	return "", fmt.Errorf("unknown channel %q (use stable, edge or canary)", name)
}

// loadChannel reads a channel source strictly: unknown fields, trailing data
// and anything validateChannel refuses are errors, so a hand edit that CI
// would sign wrongly fails here instead.
func loadChannel(path, name string) (release.ChannelManifest, error) {
	var m release.ChannelManifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return m, fmt.Errorf("%s: unexpected data after the JSON document", path)
	}
	if err := validateChannel(m, name); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// validateChannel checks the parts of a channel source people edit by hand.
func validateChannel(m release.ChannelManifest, name string) error {
	if m.Channel != name {
		return fmt.Errorf("channel is %q, want %q", m.Channel, name)
	}
	seen := map[string]bool{}
	for _, en := range m.Releases {
		if !release.ValidVersion(en.Version) {
			return fmt.Errorf("release version %q is not a version like v1.2.3", en.Version)
		}
		if seen[en.Version] {
			return fmt.Errorf("%s is listed twice", en.Version)
		}
		seen[en.Version] = true
		if id, sig, err := release.SplitRecordSig(en.RecordSig); err != nil {
			return fmt.Errorf("%s: record_sig: %w", en.Version, err)
		} else if _, err := base64.StdEncoding.DecodeString(sig); err != nil || strings.ContainsAny(id, " \t:") {
			return fmt.Errorf("%s: record_sig must be <key_id>:<base64 signature>", en.Version)
		}
		if en.HaltReason != "" && !en.Halted {
			return fmt.Errorf("%s: halt_reason is set but the release is not halted", en.Version)
		}
		if err := validateRamp(en.Ramp); err != nil {
			return fmt.Errorf("%s: %w", en.Version, err)
		}
	}
	for _, y := range m.Yanked {
		if !release.ValidVersion(y.Version) || (y.To != "" && !release.ValidVersion(y.To)) {
			return fmt.Errorf("yanked %q (to %q) is not a version like v1.2.3", y.Version, y.To)
		}
		if !seen[y.Version] {
			return fmt.Errorf("yanked %s is not in this channel", y.Version)
		}
		if y.To != "" && !seen[y.To] {
			return fmt.Errorf("yanked %s points to %s, which is not in this channel", y.Version, y.To)
		}
	}
	return nil
}

// validateRamp: at least one step, RFC 3339 times that strictly rise,
// percentages 1..100 that never fall (as parseRamp demands).
func validateRamp(steps []release.RampStep) error {
	if len(steps) == 0 {
		return errors.New("ramp needs at least one step")
	}
	var prevAt time.Time
	prevPct := 0
	for i, s := range steps {
		at, err := time.Parse(time.RFC3339, s.At)
		if err != nil {
			return fmt.Errorf("ramp step %d: from %q is not an RFC 3339 time", i+1, s.At)
		}
		if s.Pct < 1 || s.Pct > 100 || s.Pct < prevPct {
			return fmt.Errorf("ramp step %d: pct %d must be 1 to 100 and never fall", i+1, s.Pct)
		}
		if i > 0 && !at.After(prevAt) {
			return fmt.Errorf("ramp step %d: time must come after the previous step", i+1)
		}
		prevAt, prevPct = at, s.Pct
	}
	return nil
}

// marshalChannel renders m as pretty JSON with a trailing newline; empty lists
// are written as [] rather than null.
func marshalChannel(m release.ChannelManifest) ([]byte, error) {
	if m.Releases == nil {
		m.Releases = []release.Entry{}
	}
	if m.Yanked == nil {
		m.Yanked = []release.Yank{}
	}
	for i := range m.Releases {
		if m.Releases[i].Ramp == nil {
			m.Releases[i].Ramp = []release.RampStep{}
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func saveChannel(path string, m release.ChannelManifest) error {
	b, err := marshalChannel(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func findEntry(m *release.ChannelManifest, version string) *release.Entry {
	for i := range m.Releases {
		if m.Releases[i].Version == version {
			return &m.Releases[i]
		}
	}
	return nil
}

func withEntry(m *release.ChannelManifest, version string, fn func(*release.Entry)) error {
	en := findEntry(m, version)
	if en == nil {
		return fmt.Errorf("%s is not in this channel", version)
	}
	fn(en)
	return nil
}

func addRelease(m *release.ChannelManifest, version, recordFile, sigFile, rampSpec string, e env) error {
	if findEntry(m, version) != nil {
		return fmt.Errorf("%s is already in this channel; use ramp, halt or yank", version)
	}
	recordBytes, err := os.ReadFile(recordFile)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(sigFile)
	if err != nil {
		return err
	}
	recordSig := strings.TrimSpace(string(sig))
	if err := release.VerifyRecordSig(e.releaseTrust(), recordBytes, recordSig); err != nil {
		return fmt.Errorf("%s: %w", sigFile, err)
	}
	var rec release.Record
	if err := json.Unmarshal(recordBytes, &rec); err != nil {
		return fmt.Errorf("%s: %w", recordFile, err)
	}
	if rec.Version != version {
		return fmt.Errorf("%s describes %s, not %s", recordFile, rec.Version, version)
	}
	ramp, err := parseRamp(rampSpec, e.now())
	if err != nil {
		return err
	}
	m.Releases = append(m.Releases, release.Entry{Version: version, RecordSig: recordSig, Ramp: ramp})
	return nil
}

func yankRelease(m *release.ChannelManifest, version, reason, to string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("channel yank needs --reason")
	}
	if findEntry(m, version) == nil {
		return fmt.Errorf("%s is not in this channel; nothing to yank", version)
	}
	if to != "" {
		if !release.ValidVersion(to) {
			return fmt.Errorf("--to %q is not a version like v1.2.3", to)
		}
		if findEntry(m, to) == nil {
			return fmt.Errorf("--to %s is not in this channel; add it first", to)
		}
		if packages.CompareVersions(to, version) <= 0 {
			return fmt.Errorf("--to %s must be newer than %s", to, version)
		}
	}
	for _, y := range m.Yanked {
		if y.Version == version {
			return fmt.Errorf("%s is already yanked", version)
		}
	}
	m.Yanked = append(m.Yanked, release.Yank{Version: version, Reason: reason, To: to})
	return nil
}

// parseRamp reads "now:5,+2d:25,+4d:100". "now" is the invocation time floored
// to the minute; +Nd and +Nh are offsets from it. Percentages are 1..100 and
// never fall; times strictly rise.
func parseRamp(spec string, now time.Time) ([]release.RampStep, error) {
	base := now.UTC().Truncate(time.Minute)
	var steps []release.RampStep
	var prevAt time.Time
	prevPct := 0
	for i, part := range strings.Split(spec, ",") {
		at, pctText, ok := strings.Cut(strings.TrimSpace(part), ":")
		if !ok {
			return nil, fmt.Errorf("ramp step %q: want TIME:PERCENT", part)
		}
		when, err := parseRampTime(at, base)
		if err != nil {
			return nil, fmt.Errorf("ramp step %q: %w", part, err)
		}
		pct, err := strconv.Atoi(pctText)
		if err != nil || pct < 1 || pct > 100 {
			return nil, fmt.Errorf("ramp step %q: percent must be 1 to 100", part)
		}
		if pct < prevPct {
			return nil, fmt.Errorf("ramp step %q: percent falls from %d", part, prevPct)
		}
		if i > 0 && !when.After(prevAt) {
			return nil, fmt.Errorf("ramp step %q: time must come after the previous step", part)
		}
		prevAt, prevPct = when, pct
		steps = append(steps, release.RampStep{At: when.Format(time.RFC3339), Pct: pct})
	}
	return steps, nil
}

func parseRampTime(s string, base time.Time) (time.Time, error) {
	if s == "now" {
		return base, nil
	}
	if !strings.HasPrefix(s, "+") || len(s) < 3 {
		return time.Time{}, errors.New("time must be now, +Nd or +Nh")
	}
	n, err := strconv.Atoi(s[1 : len(s)-1])
	if err != nil || n < 0 {
		return time.Time{}, errors.New("time must be now, +Nd or +Nh")
	}
	switch s[len(s)-1] {
	case 'd':
		return base.Add(time.Duration(n) * 24 * time.Hour), nil
	case 'h':
		return base.Add(time.Duration(n) * time.Hour), nil
	}
	return time.Time{}, errors.New("time must be now, +Nd or +Nh")
}

// resignChannel reads <in>/<channel>.json, stamps issued_at and expires_at,
// signs it with the manifest key and writes <out>/<channel>.json. The source
// file is not touched.
func resignChannel(in, name, out, keyFile string, stdout io.Writer, e env) error {
	src, err := channelPath(in, name)
	if err != nil {
		return err
	}
	m, err := loadChannel(src, name)
	if err != nil {
		return err
	}
	s, err := newFileSigner(keyFile)
	if err != nil {
		return err
	}
	trusted := e.Manifest
	if trusted == nil {
		trusted = release.ManifestTrust()
	}
	id := packages.KeyID(s.PublicKey())
	if _, ok := trusted[id]; !ok {
		return fmt.Errorf("key %s is not a trusted manifest key; refusing to sign", id)
	}
	m.Channel = name
	issued := e.now().UTC().Truncate(time.Second)
	m.IssuedAt = issued.Format(time.RFC3339)
	m.ExpiresAt = issued.Add(manifestLifetime).Format(time.RFC3339)
	index, err := marshalChannel(m)
	if err != nil {
		return err
	}
	doc, err := packages.SignIndex(bytes.TrimSpace(index), s.key)
	if err != nil {
		return err
	}
	if _, err := release.VerifyManifest(doc, e.options(name)); err != nil {
		return fmt.Errorf("the signed manifest does not verify: %w", err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(out, name+".json")
	if err := os.WriteFile(dst, append(doc, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "signed %s with key %s -> %s (expires %s)\n", name, id, dst, m.ExpiresAt)
	return nil
}
