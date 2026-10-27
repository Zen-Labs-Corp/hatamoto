package release

import (
	"bytes"
	"testing"
)

// FuzzVerify mutates every input of Verify around a valid fixture. Nothing may
// panic, and a success is only possible with the signed record bytes and the
// recorded artifact: the signature and the sha256 pin those exactly.
func FuzzVerify(f *testing.F) {
	fx := newFixture(f)
	f.Add(fx.manifest, "v1.2.3", fx.record, fx.platform, fx.artifact)
	f.Add(fx.manifest, "v1.2.3", fx.record, "darwin-arm64", fx.artifact)
	f.Add(fx.manifest, "v9.9.9", fx.record, fx.platform, []byte{})
	f.Add([]byte(`{"index":{},"signer":"","algorithm":"","signature":""}`), "", []byte(`{}`), "", []byte{})
	f.Fuzz(func(t *testing.T, manifest []byte, version string, record []byte, platform string, artifact []byte) {
		v, err := Verify(manifest, version, record, platform, bytes.NewReader(artifact), fx.opts)
		if err != nil {
			return
		}
		if !bytes.Equal(record, fx.record) {
			t.Fatalf("verified a record that is not the signed one: %q", record)
		}
		if !bytes.Equal(artifact, fx.artifact) {
			t.Fatalf("verified an artifact that is not the recorded one: %q", artifact)
		}
		if v.Record.Version != version {
			t.Fatalf("verified %q but the record is %q", version, v.Record.Version)
		}
	})
}

// FuzzVerifyRecord feeds arbitrary record bytes and signature strings to the
// record check. Without the matching signature nothing is accepted.
func FuzzVerifyRecord(f *testing.F) {
	fx := newFixture(f)
	f.Add(fx.record, fx.recordSig)
	f.Add(fx.record, "")
	f.Add(fx.record, "id:")
	f.Add([]byte(`{"version":"v1.0.0"}`), fx.recordSig)
	f.Fuzz(func(t *testing.T, record []byte, sig string) {
		if _, err := VerifyRecord(record, sig, fx.opts); err == nil && !bytes.Equal(record, fx.record) {
			t.Fatalf("accepted record bytes that were not signed: %q", record)
		}
		_, _, _ = SplitRecordSig(sig)
	})
}

// FuzzVerifyManifest feeds arbitrary documents to the manifest check.
func FuzzVerifyManifest(f *testing.F) {
	fx := newFixture(f)
	f.Add(fx.manifest)
	f.Add([]byte(`{`))
	f.Add([]byte(`{"index":{"channel":"stable"},"signer":"x","algorithm":"ed25519","signature":"AA=="}`))
	f.Fuzz(func(t *testing.T, doc []byte) {
		_, _ = VerifyManifest(doc, fx.opts)
	})
}
