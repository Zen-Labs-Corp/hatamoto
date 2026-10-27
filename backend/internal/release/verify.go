package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/core/internal/packages"
)

const (
	// MaxManifestLifetime is the longest expires_at - issued_at Verify accepts.
	// The tools issue 14 days; a day of slack covers clock rounding.
	MaxManifestLifetime = 15 * 24 * time.Hour
	// MaxArtifactSize is the hard cap on an artifact, whatever its record says.
	MaxArtifactSize = 256 << 20
)

// Verification failures; test with errors.Is.
var (
	ErrManifest        = errors.New("release manifest is not valid")
	ErrExpired         = errors.New("release manifest has expired")
	ErrNotFresh        = errors.New("release manifest has no valid issued_at / expires_at")
	ErrChannelMismatch = errors.New("release manifest is for a different channel")
	ErrVersion         = errors.New("version is not in the form vMAJOR.MINOR.PATCH[-prerelease]")
	ErrNoSuchRelease   = errors.New("release is not in the channel manifest")
	ErrRecordSig       = errors.New("release record signature is not valid")
	ErrRecordMismatch  = errors.New("release record does not match the channel entry")
	ErrArtifactSize    = errors.New("artifact size does not match the release record")
	ErrArtifactHash    = errors.New("artifact sha256 does not match the release record")
)

// Options tune Verify. The zero value verifies against the embedded trust sets
// at the current time.
type Options struct {
	Channel           string            // the channel being checked, "stable", "edge" or "canary"; required
	Release, Manifest packages.TrustSet // nil means the embedded set
	Now               func() time.Time  // nil means time.Now
	MaxArtifact       int64             // 0 means MaxArtifactSize
}

// Verified is what Verify proved about a release.
type Verified struct {
	Record Record
	Entry  Entry
	Yanked *Yank // set when the manifest yanks this version; reported, not enforced
}

func (o Options) sets() (rel, man packages.TrustSet) {
	rel, man = o.Release, o.Manifest
	if rel == nil {
		rel = ReleaseTrust()
	}
	if man == nil {
		man = ManifestTrust()
	}
	return rel, man
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// VerifyManifest checks a signed channel manifest: the envelope signature
// against the manifest keys, then its validity window.
func VerifyManifest(manifestDoc []byte, o Options) (*ChannelManifest, error) {
	_, man := o.sets()
	payload, err := man.VerifyIndex(manifestDoc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	var m ChannelManifest
	if err := json.Unmarshal(payload, &m); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	if !slices.Contains(Channels, o.Channel) {
		return nil, fmt.Errorf("%w: Options.Channel must be one of %v, got %q", ErrChannelMismatch, Channels, o.Channel)
	}
	if m.Channel != o.Channel {
		return nil, fmt.Errorf("%w: manifest is for %q, want %q", ErrChannelMismatch, m.Channel, o.Channel)
	}
	if err := packages.CheckFresh("release manifest", m.IssuedAt, m.ExpiresAt, o.now(), MaxManifestLifetime); err != nil {
		// Say "release manifest", not the registry wording packages.CheckFresh uses.
		kind := ErrNotFresh
		if errors.Is(err, packages.ErrIndexExpired) {
			kind = ErrExpired
		}
		return nil, fmt.Errorf("%w (issued %q, expires %q)", kind, m.IssuedAt, m.ExpiresAt)
	}
	return &m, nil
}

// SplitRecordSig splits "<key_id>:<base64 signature>".
func SplitRecordSig(s string) (keyID, sig string, err error) {
	keyID, sig, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || keyID == "" || sig == "" {
		return "", "", fmt.Errorf("%w: want <key_id>:<signature>", ErrRecordSig)
	}
	return keyID, sig, nil
}

// VerifyRecordSig checks a record signature over the exact record bytes
// against the release keys.
func VerifyRecordSig(rel packages.TrustSet, recordBytes []byte, recordSig string) error {
	id, sig, err := SplitRecordSig(recordSig)
	if err != nil {
		return err
	}
	if err := rel.VerifyBytes(id, "", sig, recordBytes); err != nil {
		return fmt.Errorf("%w: %w", ErrRecordSig, err)
	}
	return nil
}

// Verify proves that version, as offered by a signed channel manifest, is the
// release recordBytes describes and that artifact (the build for platform) is
// the file the record names. Order: manifest signature, freshness, channel
// entry, record signature, record fields, artifact size and sha256.
func Verify(manifestDoc []byte, version string, recordBytes []byte, platform string, artifact io.Reader, o Options) (*Verified, error) {
	m, err := VerifyManifest(manifestDoc, o)
	if err != nil {
		return nil, err
	}
	if !ValidVersion(version) {
		return nil, fmt.Errorf("%w: %q", ErrVersion, version)
	}
	var entry *Entry
	for i := range m.Releases {
		if m.Releases[i].Version == version {
			entry = &m.Releases[i]
			break
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoSuchRelease, version)
	}
	if !ValidVersion(entry.Version) {
		return nil, fmt.Errorf("%w: entry %q", ErrVersion, entry.Version)
	}
	rec, err := VerifyRecord(recordBytes, entry.RecordSig, o)
	if err != nil {
		return nil, err
	}
	if rec.Version != entry.Version {
		return nil, fmt.Errorf("%w: record is %s, entry is %s", ErrRecordMismatch, rec.Version, entry.Version)
	}
	if err := VerifyArtifact(rec, platform, artifact, o); err != nil {
		return nil, err
	}
	v := &Verified{Record: *rec, Entry: *entry}
	for i := range m.Yanked {
		if m.Yanked[i].Version == version {
			v.Yanked = &m.Yanked[i]
			break
		}
	}
	return v, nil
}

// VerifyRecord checks recordSig ("<key_id>:<base64>") over the exact
// recordBytes against the release keys and decodes the record. It needs no
// channel manifest, so an offline apply can use it too.
func VerifyRecord(recordBytes []byte, recordSig string, o Options) (*Record, error) {
	rel, _ := o.sets()
	if err := VerifyRecordSig(rel, recordBytes, recordSig); err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(recordBytes, &rec); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRecordMismatch, err)
	}
	for name, v := range map[string]string{"version": rec.Version, "rollback_floor": rec.RollbackFloor, "min_from": rec.MinFrom} {
		if !ValidVersion(v) {
			return nil, fmt.Errorf("%w: %s %q", ErrVersion, name, v)
		}
	}
	return &rec, nil
}

// VerifyArtifact checks that r holds exactly the build rec names for
// platform: size within the cap, then sha256. At most size+1 bytes are read.
func VerifyArtifact(rec *Record, platform string, r io.Reader, o Options) error {
	art, ok := rec.Artifacts[platform]
	if !ok {
		return fmt.Errorf("%w: no artifact for %s", ErrRecordMismatch, platform)
	}
	return checkArtifact(art, r, o)
}

func checkArtifact(art Artifact, r io.Reader, o Options) error {
	limit := o.MaxArtifact
	if limit <= 0 {
		limit = MaxArtifactSize
	}
	if art.Size < 0 || art.Size > limit {
		return fmt.Errorf("%w: record says %d bytes, the limit is %d", ErrArtifactSize, art.Size, limit)
	}
	h := sha256.New()
	// One byte past the recorded size is enough to see a longer stream.
	n, err := io.Copy(h, io.LimitReader(r, art.Size+1))
	if err != nil {
		return fmt.Errorf("read artifact: %w", err)
	}
	if n != art.Size {
		return fmt.Errorf("%w: got %d bytes, record says %d", ErrArtifactSize, n, art.Size)
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), art.SHA256) {
		return ErrArtifactHash
	}
	return nil
}
