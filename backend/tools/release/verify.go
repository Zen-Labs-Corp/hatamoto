package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hatamoto.dev/core/internal/jsonx"
	"hatamoto.dev/core/internal/release"
)

// cmdVerify runs release.VerifyManifest or the full release.Verify against the
// embedded trust sets.
func cmdVerify(args []string, stdout, stderr io.Writer, e env) error {
	var manifest, channel, chanName, record, sigFile, artifact, platform, dir, version string
	var listFiles bool
	fs := newFlags("verify", stderr)
	fs.StringVar(&manifest, "manifest", "", "check only this signed channel file")
	fs.StringVar(&channel, "channel", "", "signed channel file")
	fs.StringVar(&chanName, "chan", "", "channel the file must be for: stable, edge or canary")
	fs.StringVar(&record, "record", "", "release record")
	fs.StringVar(&sigFile, "sig", "", "detached record signature")
	fs.StringVar(&artifact, "artifact", "", "the binary to check")
	fs.StringVar(&platform, "platform", "", "platform of the artifact, e.g. linux-amd64")
	fs.StringVar(&dir, "dir", "", "record-only mode: directory holding hatamoto-<platform> for every platform")
	fs.StringVar(&version, "version", "", "record-only mode: the version the record must be for")
	fs.BoolVar(&listFiles, "list-files", false, "record-only mode: after verifying, print only the artifact file names the record names, one per line")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if chanName == "" {
		return errors.New("verify needs --chan stable|edge|canary")
	}
	if manifest != "" {
		doc, err := os.ReadFile(manifest)
		if err != nil {
			return err
		}
		m, err := release.VerifyManifest(doc, e.options(chanName))
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "manifest ok: issued %s, expires %s, %d release(s), %d yank(s)\n", m.IssuedAt, m.ExpiresAt, len(m.Releases), len(m.Yanked))
		return nil
	}
	if channel == "" && dir != "" && record != "" && sigFile != "" && artifact == "" && platform == "" {
		return verifyRecordDir(stdout, e.options(chanName), record, sigFile, dir, version, listFiles)
	}
	if listFiles {
		return errors.New("--list-files belongs to record-only mode: verify --chan C --record F --sig F --dir D [--version V] --list-files")
	}
	if dir != "" || version != "" {
		return errors.New("--dir and --version belong to record-only mode: verify --chan C --record F --sig F --dir D [--version V]")
	}
	if channel == "" || record == "" || sigFile == "" || artifact == "" || platform == "" {
		return errors.New("usage: verify --chan stable|edge|canary (--manifest F | --channel F --record F --sig F --artifact F --platform P | --record F --sig F --dir D [--version V])")
	}
	doc, err := os.ReadFile(channel)
	if err != nil {
		return err
	}
	recordBytes, err := os.ReadFile(record)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(sigFile)
	if err != nil {
		return err
	}
	var rec release.Record
	if err := jsonx.Unmarshal(recordBytes, &rec, jsonx.Stored); err != nil {
		return fmt.Errorf("%s: %w", record, err)
	}
	opts := e.options(chanName)
	if err := release.VerifyRecordSig(e.releaseTrust(), recordBytes, strings.TrimSpace(string(sig))); err != nil {
		return fmt.Errorf("%s: %w", sigFile, err)
	}
	f, err := os.Open(artifact)
	if err != nil {
		return err
	}
	defer f.Close()
	v, err := release.Verify(doc, rec.Version, recordBytes, platform, f, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "release ok: %s for %s (schema_head %d)\n", v.Record.Version, platform, v.Record.SchemaHead)
	if v.Yanked != nil {
		fmt.Fprintf(stdout, "note: %s is yanked: %s\n", v.Yanked.Version, v.Yanked.Reason)
	}
	if v.Entry.Halted {
		fmt.Fprintln(stdout, "note: the channel entry is halted")
	}
	return nil
}

// verifyRecordDir checks a record, its signature and every platform file in
// dir without a channel manifest, so a release can be verified before it is
// offered on a channel. With list it prints only the artifact file names the
// record names (default platforms first, then each listed edition).
func verifyRecordDir(stdout io.Writer, opts release.Options, record, sigFile, dir, version string, list bool) error {
	recordBytes, err := os.ReadFile(record)
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(sigFile)
	if err != nil {
		return err
	}
	rec, err := release.VerifyRecord(recordBytes, strings.TrimSpace(string(sig)), opts)
	if err != nil {
		return fmt.Errorf("%s: %w", record, err)
	}
	if version != "" && rec.Version != version {
		return fmt.Errorf("record is for %s, expected %s", rec.Version, version)
	}
	for _, platform := range release.Platforms {
		if _, ok := rec.Artifacts[platform]; !ok {
			return fmt.Errorf("record has no artifact for %s", platform)
		}
	}
	if len(rec.Artifacts) != len(release.Platforms) {
		return fmt.Errorf("record names %d artifacts, expected %d", len(rec.Artifacts), len(release.Platforms))
	}
	for ed, arts := range rec.Editions {
		if !contains(release.Editions, ed) {
			return fmt.Errorf("record names unknown edition %q", ed)
		}
		if len(arts) != len(release.Platforms) {
			return fmt.Errorf("record names %d %s artifacts, expected %d", len(arts), ed, len(release.Platforms))
		}
		for _, platform := range release.Platforms {
			if _, ok := arts[platform]; !ok {
				return fmt.Errorf("record has no %s artifact for %s", ed, platform)
			}
		}
	}
	// Exact probes only: dir also holds bundles, SBOMs and checksums.
	for _, ed := range release.Editions {
		if _, ok := rec.Editions[ed]; ok {
			continue
		}
		for _, platform := range release.Platforms {
			if name := release.ArtifactName(ed, platform); fileExists(filepath.Join(dir, name)) {
				return fmt.Errorf("%s is in %s but the record names no %s builds", name, dir, ed)
			}
		}
	}
	var names []string
	for _, platform := range release.Platforms {
		name := release.ArtifactName("", platform)
		if err := verifyArtifactFile(rec, "", platform, filepath.Join(dir, name), opts); err != nil {
			return fmt.Errorf("%s: %w", filepath.Join(dir, name), err)
		}
		names = append(names, name)
	}
	edArtifacts := 0
	for _, ed := range release.Editions {
		if _, ok := rec.Editions[ed]; !ok {
			continue
		}
		for _, platform := range release.Platforms {
			name := release.ArtifactName(ed, platform)
			if err := verifyArtifactFile(rec, ed, platform, filepath.Join(dir, name), opts); err != nil {
				return fmt.Errorf("%s: %w", filepath.Join(dir, name), err)
			}
			names = append(names, name)
			edArtifacts++
		}
	}
	switch {
	case list:
		for _, name := range names {
			fmt.Fprintln(stdout, name)
		}
	case edArtifacts == 0:
		fmt.Fprintf(stdout, "record ok: %s (%d artifacts)\n", rec.Version, len(rec.Artifacts))
	default:
		fmt.Fprintf(stdout, "record ok: %s (%d artifacts, %d edition artifacts)\n", rec.Version, len(rec.Artifacts), edArtifacts)
	}
	return nil
}

func verifyArtifactFile(rec *release.Record, edition, platform, name string, opts release.Options) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return release.VerifyArtifactFor(rec, edition, platform, f, opts)
}
