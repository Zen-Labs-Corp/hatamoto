package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
)

var testNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// fixture is a throwaway release set: two key pairs, a signed record and a
// signed manifest offering it.
type fixture struct {
	relKey, manKey packages.Key
	opts           Options
	artifact       []byte
	record         []byte
	recordSig      string
	manifest       []byte
	platform       string
}

func genKey(t testing.TB) (packages.Key, packages.TrustSet) {
	t.Helper()
	k, err := packages.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := k.PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	ts := packages.TrustSet{}
	ts.Add(pub)
	return k, ts
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	f := &fixture{platform: "linux-amd64", artifact: []byte("pretend this is a binary")}
	var rel, man packages.TrustSet
	f.relKey, rel = genKey(t)
	f.manKey, man = genKey(t)
	f.opts = Options{Channel: "stable", Release: rel, Manifest: man, Now: func() time.Time { return testNow }}

	rec := Record{
		Version: "v1.2.3", Kind: "patch", SDK: "0.10.0", SchemaHead: 7,
		RollbackFloor: "v1.2.0", MinFrom: "v1.0.0", NotesURL: "https://example.test/notes",
		Artifacts: map[string]Artifact{f.platform: {SHA256: sha(f.artifact), Size: int64(len(f.artifact))}},
	}
	var err error
	if f.record, err = MarshalRecord(rec); err != nil {
		t.Fatal(err)
	}
	f.recordSig = f.signRecord(t, f.relKey, f.record)
	f.manifest = f.signManifest(t, f.manKey, f.manifestBody(testNow.Add(-time.Hour), 14*24*time.Hour))
	return f
}

func (f *fixture) signRecord(t testing.TB, k packages.Key, rec []byte) string {
	t.Helper()
	s, err := packages.SignBytes(k, rec)
	if err != nil {
		t.Fatal(err)
	}
	return s.Signer + ":" + s.Signature
}

func (f *fixture) manifestBody(issued time.Time, life time.Duration) ChannelManifest {
	return ChannelManifest{
		Channel:   "stable",
		IssuedAt:  issued.Format(time.RFC3339),
		ExpiresAt: issued.Add(life).Format(time.RFC3339),
		Releases:  []Entry{{Version: "v1.2.3", RecordSig: f.recordSig, Ramp: []RampStep{{At: testNow.Format(time.RFC3339), Pct: 100}}}},
		Yanked:    []Yank{},
	}
}

func (f *fixture) signManifest(t testing.TB, k packages.Key, m ChannelManifest) []byte {
	t.Helper()
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := packages.SignIndex(body, k)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
