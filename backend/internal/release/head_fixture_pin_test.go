package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
)

// The files under testdata/head were produced by the encoder in place before
// the platform JSON cutover, with a throwaway key derived from pinSeed (it signs
// nothing real). They are never regenerated: they prove that what an older
// build signed and published still verifies.
const pinSeed = "hatamoto-json-v2-4-release-pin"

func pinKey() packages.Key {
	seed := sha256.Sum256([]byte(pinSeed))
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub := priv.Public().(ed25519.PublicKey)
	return packages.Key{ID: packages.KeyID(pub), Public: base64.StdEncoding.EncodeToString(pub), Private: base64.StdEncoding.EncodeToString(priv)}
}

func headFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/head/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// headOptions trusts the checked-in public key for both record and manifest.
func headOptions(t *testing.T) Options {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(headFile(t, "trust.pub"))))
	if err != nil {
		t.Fatal(err)
	}
	ts := packages.TrustSet{}
	ts.Add(ed25519.PublicKey(raw))
	return Options{Channel: "stable", Release: ts, Manifest: ts, Now: func() time.Time { return testNow }}
}

func TestHeadSignedRecordVerifies(t *testing.T) {
	rec, err := VerifyRecord(headFile(t, "release.json"), string(headFile(t, "release.json.sig")), headOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Version != "v1.2.3" || rec.Kind != "patch" || !rec.Security || rec.SDK != "0.10.0" || rec.SchemaHead != 7 ||
		rec.RollbackFloor != "v1.2.0" || rec.MinFrom != "v1.0.0" || rec.NotesURL != "https://example.test/notes" ||
		strings.Join(rec.SDKCompatibleWith, ",") != "0.9.0,0.8.0" || len(rec.Artifacts) != 2 ||
		rec.Artifacts["linux-arm64"] != (Artifact{SHA256: "3e23e8160039594a33894f6564e1b1348bbd7a0088d42c4acb73eeaed59c009d", Size: 1}) {
		t.Fatalf("record = %+v", rec)
	}
}

func TestHeadSignedManifestVerifies(t *testing.T) {
	m, err := VerifyManifest(headFile(t, "channel.manifest.json"), headOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if m.Channel != "stable" || m.IssuedAt != "2026-11-01T11:00:00Z" || m.ExpiresAt != "2026-11-15T11:00:00Z" || len(m.Releases) != 2 || len(m.Yanked) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	if e := m.Releases[0]; e.Version != "v1.2.3" || e.RecordSig != string(headFile(t, "release.json.sig")) || e.Halted ||
		len(e.Ramp) != 1 || e.Ramp[0] != (RampStep{At: "2026-11-01T12:00:00Z", Pct: 100}) {
		t.Fatalf("entry 0 = %+v", e)
	}
	if e := m.Releases[1]; e.Version != "v1.1.0" || !e.Halted || e.HaltReason != "bad & <broken>" || e.Ramp == nil || len(e.Ramp) != 0 {
		t.Fatalf("entry 1 = %+v", e)
	}
	if y := m.Yanked[0]; y != (Yank{Version: "v1.0.0", Reason: "crash & burn\u2028é", To: "v1.1.0"}) {
		t.Fatalf("yank = %+v", y)
	}
}

// The full path: manifest, record and artifact checked together.
func TestHeadSignedReleaseVerifiesEndToEnd(t *testing.T) {
	v, err := Verify(headFile(t, "channel.manifest.json"), "v1.2.3", headFile(t, "release.json"), "linux-amd64", strings.NewReader("a"), headOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if v.Record.Version != "v1.2.3" || v.Entry.Version != "v1.2.3" || v.Yanked != nil {
		t.Fatalf("verified = %+v", v)
	}
}

var pinRecords = []struct {
	name string
	rec  Record
	want string
}{
	{"normal", Record{Version: "v1.2.3", Kind: "patch", Security: true, SDK: "0.10.0", SDKCompatibleWith: []string{"0.9.0", "0.8.0"}, SchemaHead: 7, RollbackFloor: "v1.2.0", MinFrom: "v1.0.0", NotesURL: "https://example.test/notes",
		Artifacts: map[string]Artifact{"linux-arm64": {SHA256: sha([]byte("b")), Size: 1}, "linux-amd64": {SHA256: sha([]byte("a")), Size: 1}}},
		"{\n  \"version\": \"v1.2.3\",\n  \"kind\": \"patch\",\n  \"security\": true,\n  \"sdk\": \"0.10.0\",\n  \"sdk_compatible_with\": [\n    \"0.9.0\",\n    \"0.8.0\"\n  ],\n  \"schema_head\": 7,\n  \"rollback_floor\": \"v1.2.0\",\n  \"min_from\": \"v1.0.0\",\n  \"notes_url\": \"https://example.test/notes\",\n  \"artifacts\": {\n    \"linux-amd64\": {\n      \"sha256\": \"ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb\",\n      \"size\": 1\n    },\n    \"linux-arm64\": {\n      \"sha256\": \"3e23e8160039594a33894f6564e1b1348bbd7a0088d42c4acb73eeaed59c009d\",\n      \"size\": 1\n    }\n  }\n}\n"},
	{"html, U+2028 and empty sdk_compatible_with", Record{Version: "v1.3.0-rc.1", Kind: "minor", SDK: "0.10.0", SDKCompatibleWith: []string{}, SchemaHead: 8, RollbackFloor: "v1.2.0", MinFrom: "v1.0.0", NotesURL: "https://x/?a=1&b=2\u2028é<>",
		Artifacts: map[string]Artifact{"linux-amd64": {SHA256: sha([]byte("a")), Size: 1}}},
		"{\n  \"version\": \"v1.3.0-rc.1\",\n  \"kind\": \"minor\",\n  \"security\": false,\n  \"sdk\": \"0.10.0\",\n  \"schema_head\": 8,\n  \"rollback_floor\": \"v1.2.0\",\n  \"min_from\": \"v1.0.0\",\n  \"notes_url\": \"https://x/?a=1\\u0026b=2\\u2028é\\u003c\\u003e\",\n  \"artifacts\": {\n    \"linux-amd64\": {\n      \"sha256\": \"ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb\",\n      \"size\": 1\n    }\n  }\n}\n"},
	{"nil artifacts", Record{Version: "v2.0.0", Kind: "major", SDK: "1.0.0", SchemaHead: 9, RollbackFloor: "v1.3.0", MinFrom: "v1.3.0"},
		"{\n  \"version\": \"v2.0.0\",\n  \"kind\": \"major\",\n  \"security\": false,\n  \"sdk\": \"1.0.0\",\n  \"schema_head\": 9,\n  \"rollback_floor\": \"v1.3.0\",\n  \"min_from\": \"v1.3.0\",\n  \"notes_url\": \"\",\n  \"artifacts\": null\n}\n"},
}

// The signed bytes: sorted artifact keys, HTML characters and U+2028 escaped,
// nil artifacts as null, an empty sdk_compatible_with omitted.
func TestMarshalRecordGolden(t *testing.T) {
	for _, c := range pinRecords {
		t.Run(c.name, func(t *testing.T) {
			got, err := MarshalRecord(c.rec)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("record bytes changed:\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

// Signing is deterministic, so the same key over the same bytes reproduces the
// checked-in signature.
func TestResignReproducesSig(t *testing.T) {
	b, err := MarshalRecord(pinRecords[0].rec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, headFile(t, "release.json")) {
		t.Fatal("MarshalRecord no longer reproduces testdata/head/release.json")
	}
	s, err := packages.SignBytes(pinKey(), b)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Signer + ":" + s.Signature; got != string(headFile(t, "release.json.sig")) {
		t.Fatalf("signature changed: %s", got)
	}
}

// Documents are decoded after their signature is checked, as the previous
// decoder did: member names match without case, a repeated or unknown member is
// ignored.
func TestVerifyIgnoresCaseAndRepeatedMembers(t *testing.T) {
	o := headOptions(t)
	rec := `{"VERSION":"v1.2.3","kind":"patch","kind":"patch","security":false,"sdk":"0.10.0","schema_head":7,"rollback_floor":"v1.2.0","min_from":"v1.0.0","notes_url":"","artifacts":null,"unknown":1,"unknown":2}`
	s, err := packages.SignBytes(pinKey(), []byte(rec))
	if err != nil {
		t.Fatal(err)
	}
	r, err := VerifyRecord([]byte(rec), s.Signer+":"+s.Signature, o)
	if err != nil || r.Version != "v1.2.3" || r.Kind != "patch" {
		t.Fatalf("record: %+v %v", r, err)
	}
	body := `{"CHANNEL":"stable","channel":"stable","issued_at":"2026-11-01T11:00:00Z","expires_at":"2026-11-15T11:00:00Z","releases":[],"yanked":[],"extra":true,"extra":false}`
	doc, err := packages.SignIndex([]byte(body), pinKey())
	if err != nil {
		t.Fatal(err)
	}
	m, err := VerifyManifest(doc, o)
	if err != nil || m.Channel != "stable" {
		t.Fatalf("manifest: %+v %v", m, err)
	}
}
