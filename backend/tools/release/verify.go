package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"hatamoto.dev/core/internal/release"
)

// cmdVerify runs release.VerifyManifest or the full release.Verify against the
// embedded trust sets.
func cmdVerify(args []string, stdout, stderr io.Writer, e env) error {
	var manifest, channel, chanName, record, sigFile, artifact, platform, dir, version string
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
		return verifyRecordDir(stdout, e.options(chanName), record, sigFile, dir, version)
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
	if err := json.Unmarshal(recordBytes, &rec); err != nil {
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
// offered on a channel.
func verifyRecordDir(stdout io.Writer, opts release.Options, record, sigFile, dir, version string) error {
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
	for _, platform := range release.Platforms {
		name := filepath.Join(dir, "hatamoto-"+platform)
		if err := verifyArtifactFile(rec, platform, name, opts); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	fmt.Fprintf(stdout, "record ok: %s (%d artifacts)\n", rec.Version, len(rec.Artifacts))
	return nil
}

func verifyArtifactFile(rec *release.Record, platform, name string, opts release.Options) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return release.VerifyArtifact(rec, platform, f, opts)
}
