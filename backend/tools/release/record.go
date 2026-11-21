package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh/agent"

	"hatamoto.dev/core/internal/editionstate"
	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/release"
)

// facts is the output of `hatamoto version --json`.
type facts struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	SDK        string `json:"sdk"`
	SchemaHead int64  `json:"schema_head"`
	Edition    string `json:"edition,omitempty"`

	SDKCompatibleWith  []string `json:"sdk_compatible_with,omitempty"`
	RemovedPermissions []string `json:"removed_permissions,omitempty"`
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

func cmdRecord(args []string, stdout, stderr io.Writer, _ env) error {
	var (
		version, kind, factsFile, floor, minFrom, notes, dir, out, check, amend string
		security, allowSame                                                     bool
	)
	fs := newFlags("record", stderr)
	fs.StringVar(&version, "version", "", "release version, e.g. v1.2.3")
	fs.StringVar(&kind, "kind", "", "patch, minor or major")
	fs.BoolVar(&security, "security", false, "mark the release as a security fix")
	fs.StringVar(&factsFile, "facts", "", "output of `hatamoto version --json`")
	fs.StringVar(&floor, "rollback-floor", "", "oldest version a rollback may return to")
	fs.StringVar(&minFrom, "min-from", "", "oldest version that may update straight to this one")
	fs.StringVar(&notes, "notes-url", "", "release notes URL")
	fs.StringVar(&dir, "dir", "", "directory holding hatamoto-<platform> files (and hatamoto-<edition>-<platform> files)")
	fs.StringVar(&out, "out", "release.json", "record file to write")
	fs.StringVar(&check, "check", "", "check an existing record instead of building one")
	fs.StringVar(&amend, "amend", "", "edit an unsigned record (with --security, --kind, --rollback-floor, --min-from) and re-check it")
	fs.BoolVar(&allowSame, "allow-same", false, "accept rollback_floor/min_from equal to version")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if amend != "" {
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		return amendRecord(amend, set, kind, security, floor, minFrom, allowSame, stdout)
	}
	if check != "" {
		return checkRecord(check, dir, allowSame, stdout)
	}
	rec, err := buildRecord(version, kind, security, factsFile, floor, minFrom, notes, dir)
	if err != nil {
		return err
	}
	b, err := release.MarshalRecord(rec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s (unsigned); review it with: record --check %s --dir %s\n", out, out, dir)
	return nil
}

// amendRecord changes the operator's fields of an unsigned record, re-runs the
// record checks and only then rewrites the file. Fields whose flag is absent
// are left alone.
func amendRecord(path string, set map[string]bool, kind string, security bool, floor, minFrom string, allowSame bool, stdout io.Writer) error {
	rec, err := readRecord(path)
	if err != nil {
		return err
	}
	if set["kind"] {
		rec.Kind = kind
	}
	if set["security"] {
		rec.Security = security
	}
	if set["rollback-floor"] {
		rec.RollbackFloor = floor
	}
	if set["min-from"] {
		rec.MinFrom = minFrom
	}
	if problems := recordProblems(rec, allowSame); len(problems) > 0 {
		return fmt.Errorf("%s not changed: %s", path, strings.Join(problems, "; "))
	}
	b, err := release.MarshalRecord(rec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "amended %s (%s, security=%t, rollback_floor %s, min_from %s)\n", path, rec.Kind, rec.Security, rec.RollbackFloor, rec.MinFrom)
	return nil
}

func readRecord(path string) (release.Record, error) {
	var rec release.Record
	b, err := os.ReadFile(path)
	if err != nil {
		return rec, err
	}
	if err := jsonx.Unmarshal(b, &rec, jsonx.Stored); err != nil {
		return rec, fmt.Errorf("%s: %w", path, err)
	}
	return rec, nil
}

func hashFile(path string) (release.Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return release.Artifact{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return release.Artifact{}, err
	}
	return release.Artifact{SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}

func buildRecord(version, kind string, security bool, factsFile, floor, minFrom, notes, dir string) (release.Record, error) {
	var rec release.Record
	if factsFile == "" || dir == "" {
		return rec, errors.New("--facts and --dir are required")
	}
	b, err := os.ReadFile(factsFile)
	if err != nil {
		return rec, err
	}
	var f facts
	if err := jsonx.Unmarshal(b, &f, jsonx.Stored); err != nil {
		return rec, fmt.Errorf("%s: %w", factsFile, err)
	}
	if f.Version != version {
		return rec, fmt.Errorf("the binary reports version %q, not %q; rebuild with the right VERSION", f.Version, version)
	}
	if f.Commit == "" || f.Commit == "unknown" {
		return rec, errors.New("the binary reports no commit; build with COMMIT set")
	}
	if f.Edition != "" && f.Edition != editionstate.Community {
		return rec, fmt.Errorf("the facts come from a %q build; record the default build's facts", f.Edition)
	}
	rec = release.Record{
		Version: version, Kind: kind, Security: security, SDK: f.SDK, SDKCompatibleWith: f.SDKCompatibleWith, RemovedPermissions: f.RemovedPermissions, SchemaHead: f.SchemaHead,
		RollbackFloor: floor, MinFrom: minFrom, NotesURL: notes,
		Artifacts: map[string]release.Artifact{},
	}
	for _, p := range release.Platforms {
		a, err := hashFile(filepath.Join(dir, "hatamoto-"+p))
		if err != nil {
			return rec, fmt.Errorf("artifact for %s: %w", p, err)
		}
		rec.Artifacts[p] = a
	}
	if err := addEditions(&rec, dir); err != nil {
		return rec, err
	}
	if problems := recordProblems(rec, true); len(problems) > 0 {
		return rec, errors.New(strings.Join(problems, "; "))
	}
	return rec, nil
}

// addEditions records the files of every listed edition found in dir: all
// platforms or none. Only exact file names are probed, so bundles and other
// files in dir are never picked up.
func addEditions(rec *release.Record, dir string) error {
	for _, ed := range release.Editions {
		found := 0
		for _, p := range release.Platforms {
			ok, err := fileExists(filepath.Join(dir, release.ArtifactName(ed, p)))
			if err != nil {
				return err
			}
			if ok {
				found++
			}
		}
		if found == 0 {
			continue
		}
		if found != len(release.Platforms) {
			return fmt.Errorf("found %d of %d %s builds in %s; provide all or none", found, len(release.Platforms), ed, dir)
		}
		arts := map[string]release.Artifact{}
		for _, p := range release.Platforms {
			a, err := hashFile(filepath.Join(dir, release.ArtifactName(ed, p)))
			if err != nil {
				return fmt.Errorf("%s artifact for %s: %w", ed, p, err)
			}
			arts[p] = a
		}
		if rec.Editions == nil {
			rec.Editions = map[string]map[string]release.Artifact{}
		}
		rec.Editions[ed] = arts
	}
	return nil
}

// fileExists reports whether path names a regular file, following symlinks
// like hashFile does. Only a missing path is "absent"; a dangling link, a
// directory, another non-regular file or an unreadable entry is an error.
func fileExists(path string) (bool, error) {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", path)
	}
	return true, nil
}

// recordProblems lists what is wrong with rec; allowSame accepts a floor or
// min_from equal to the version.
func recordProblems(rec release.Record, allowSame bool) []string {
	var p []string
	add := func(format string, a ...any) { p = append(p, fmt.Sprintf(format, a...)) }
	versionOK := release.ValidVersion(rec.Version)
	if !versionOK {
		add("version %q is not a valid version (want vMAJOR.MINOR.PATCH[-prerelease])", rec.Version)
	}
	switch rec.Kind {
	case "patch", "minor", "major":
		if want := kindOf(rec.Version); versionOK && rec.Kind != want {
			add("kind %s does not match version %s (want %s)", rec.Kind, rec.Version, want)
		}
	default:
		add("kind %q is not patch, minor or major", rec.Kind)
	}
	if !packages.ValidSemver(rec.SDK) {
		add("sdk %q is not a valid version", rec.SDK)
	}
	for _, c := range rec.SDKCompatibleWith {
		if !packages.ValidSemver(c) {
			add("sdk_compatible_with entry %q is not a valid version", c)
		}
	}
	for i, r := range rec.RemovedPermissions {
		switch {
		case r == "":
			add("removed_permissions entry %d is empty", i)
		case i > 0 && r <= rec.RemovedPermissions[i-1]:
			add("removed_permissions must be sorted and unique (%q after %q)", r, rec.RemovedPermissions[i-1])
		}
	}
	if rec.SchemaHead <= 0 {
		add("schema_head %d must be positive", rec.SchemaHead)
	}
	if rec.NotesURL == "" {
		add("notes_url is empty")
	}
	for name, v := range map[string]string{"rollback_floor": rec.RollbackFloor, "min_from": rec.MinFrom} {
		switch {
		case !release.ValidVersion(v):
			add("%s %q is not a valid version (want vMAJOR.MINOR.PATCH[-prerelease])", name, v)
		case !versionOK:
		case packages.CompareVersions(v, rec.Version) > 0:
			add("%s %s is newer than the version %s", name, v, rec.Version)
		case v == rec.Version && !allowSame:
			add("%s is still the placeholder %s (set it, or pass --allow-same)", name, v)
		}
	}
	for _, plat := range release.Platforms {
		if _, ok := rec.Artifacts[plat]; !ok {
			add("no artifact for %s", plat)
		}
	}
	for plat, a := range rec.Artifacts {
		if !contains(release.Platforms, plat) {
			add("unknown platform %q", plat)
		}
		if !sha256Hex.MatchString(a.SHA256) {
			add("%s: sha256 is not 64 lowercase hex characters", plat)
		}
		if a.Size <= 0 || a.Size > release.MaxArtifactSize {
			add("%s: size %d is outside 1..%d", plat, a.Size, int64(release.MaxArtifactSize))
		}
	}
	for _, ed := range sortedKeys(rec.Editions) {
		if !contains(release.Editions, ed) {
			add("unknown edition %q", ed)
			continue
		}
		arts := rec.Editions[ed]
		for _, plat := range release.Platforms {
			if _, ok := arts[plat]; !ok {
				add("%s: no artifact for %s", ed, plat)
			}
		}
		for _, plat := range sortedKeys(arts) {
			a := arts[plat]
			if !contains(release.Platforms, plat) {
				add("%s: unknown platform %q", ed, plat)
			}
			if !sha256Hex.MatchString(a.SHA256) {
				add("%s/%s: sha256 is not 64 lowercase hex characters", ed, plat)
			}
			if a.Size <= 0 || a.Size > release.MaxArtifactSize {
				add("%s/%s: size %d is outside 1..%d", ed, plat, a.Size, int64(release.MaxArtifactSize))
			}
		}
	}
	return p
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// kindOf names the release kind a canonical version implies: x.0.0 is major,
// x.y.0 with y above 0 is minor, anything with a patch number is patch.
func kindOf(version string) string {
	core, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), "-")
	parts := strings.Split(core, ".")
	switch {
	case len(parts) != 3:
		return ""
	case parts[2] != "0":
		return "patch"
	case parts[1] != "0":
		return "minor"
	}
	return "major"
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// checkRecord prints a readable summary of a record for review before signing
// and fails when anything is off.
func checkRecord(path, dir string, allowSame bool, stdout io.Writer) error {
	rec, err := readRecord(path)
	if err != nil {
		return err
	}
	problems := recordProblems(rec, allowSame)
	if dir != "" {
		for plat, a := range rec.Artifacts {
			if !contains(release.Platforms, plat) {
				continue // reported as an unknown platform; never joined into a path
			}
			got, err := hashFile(filepath.Join(dir, "hatamoto-"+plat))
			switch {
			case err != nil:
				problems = append(problems, fmt.Sprintf("%s: %v", plat, err))
			case got != a:
				problems = append(problems, fmt.Sprintf("%s: the file in %s does not match the record", plat, dir))
			}
		}
	}
	if dir != "" {
		for _, ed := range sortedKeys(rec.Editions) {
			if !contains(release.Editions, ed) {
				continue // reported as an unknown edition; never joined into a path
			}
			for _, plat := range sortedKeys(rec.Editions[ed]) {
				if !contains(release.Platforms, plat) {
					continue
				}
				got, err := hashFile(filepath.Join(dir, release.ArtifactName(ed, plat)))
				switch {
				case err != nil:
					problems = append(problems, fmt.Sprintf("%s/%s: %v", ed, plat, err))
				case got != rec.Editions[ed][plat]:
					problems = append(problems, fmt.Sprintf("%s/%s: the file in %s does not match the record", ed, plat, dir))
				}
			}
		}
	}
	fmt.Fprintf(stdout, "version        %s (%s%s)\nsdk            %s\nschema_head    %d\nrollback_floor %s\nmin_from       %s\nnotes_url      %s\n",
		rec.Version, rec.Kind, map[bool]string{true: ", security", false: ""}[rec.Security],
		rec.SDK, rec.SchemaHead, rec.RollbackFloor, rec.MinFrom, rec.NotesURL)
	if len(rec.RemovedPermissions) > 0 {
		fmt.Fprintf(stdout, "removed_perms  %s\n", strings.Join(rec.RemovedPermissions, ", "))
	}
	for _, plat := range release.Platforms {
		if a, ok := rec.Artifacts[plat]; ok {
			fmt.Fprintf(stdout, "%-14s %s %d\n", plat, a.SHA256, a.Size)
		}
	}
	for _, ed := range sortedKeys(rec.Editions) {
		for _, plat := range sortedKeys(rec.Editions[ed]) {
			a := rec.Editions[ed][plat]
			fmt.Fprintf(stdout, "%-14s %s %d\n", ed+"/"+plat, a.SHA256, a.Size)
		}
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(stdout, "PROBLEM:", p)
		}
		return fmt.Errorf("%s needs review: %d problem(s)", path, len(problems))
	}
	fmt.Fprintln(stdout, "record looks complete")
	return nil
}

func cmdSignRecord(args []string, stdout, stderr io.Writer, e env) error {
	var keyFile, agentKey, out string
	var useAgent, allowSame bool
	fs := newFlags("sign-record", stderr)
	fs.BoolVar(&allowSame, "allow-same", false, "accept rollback_floor/min_from equal to version (majors)")
	fs.StringVar(&keyFile, "key", "", "release key file")
	fs.BoolVar(&useAgent, "agent", false, "sign with an ssh-agent (SSH_AUTH_SOCK)")
	fs.StringVar(&agentKey, "agent-key", "", "key id of the agent key to use")
	fs.StringVar(&out, "out", "", "signature file (default: <record>.sig)")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || (keyFile == "") == !useAgent {
		return errors.New("usage: sign-record FILE (--key KEY | --agent [--agent-key ID]) [--out FILE]")
	}
	recordBytes, err := os.ReadFile(pos[0])
	if err != nil {
		return err
	}
	var rec release.Record
	if err := jsonx.Unmarshal(recordBytes, &rec, jsonx.Stored); err != nil {
		return fmt.Errorf("%s: %w", pos[0], err)
	}
	if problems := recordProblems(rec, allowSame); len(problems) > 0 {
		return fmt.Errorf("refusing to sign %s: %s (see record --check, record --amend)", pos[0], strings.Join(problems, "; "))
	}
	trusted := e.releaseTrust()
	var s signer
	if useAgent {
		conn, err := e.dialAgent()
		if err != nil {
			return err
		}
		defer conn.Close()
		if s, err = newAgentSigner(agent.NewClient(conn), trusted, agentKey); err != nil {
			return err
		}
	} else if s, err = newFileSigner(keyFile); err != nil {
		return err
	}
	id := packages.KeyID(s.PublicKey())
	if _, ok := trusted[id]; !ok {
		return fmt.Errorf("key %s is not a trusted release key; refusing to sign", id)
	}
	raw, err := s.Sign(recordBytes)
	if err != nil {
		return err
	}
	sig := encodeRecordSig(s.PublicKey(), raw)
	if err := release.VerifyRecordSig(trusted, recordBytes, sig); err != nil {
		return fmt.Errorf("the signature does not verify: %w", err)
	}
	if out == "" {
		out = pos[0] + ".sig"
	}
	if err := os.WriteFile(out, []byte(sig+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "signed %s with key %s -> %s\n", pos[0], id, out)
	return nil
}
