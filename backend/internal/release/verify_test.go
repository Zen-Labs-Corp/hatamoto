package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/core/internal/packages"
)

func (f *fixture) verify(manifest, record []byte, artifact io.Reader) (*Verified, error) {
	return Verify(manifest, "v1.2.3", record, f.platform, artifact, f.opts)
}

func (f *fixture) verifyGood() (*Verified, error) {
	return f.verify(f.manifest, f.record, bytes.NewReader(f.artifact))
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestVerifyOK(t *testing.T) {
	f := newFixture(t)
	v, err := f.verifyGood()
	if err != nil {
		t.Fatal(err)
	}
	if v.Record.Version != "v1.2.3" || v.Entry.Version != "v1.2.3" || v.Yanked != nil {
		t.Fatalf("unexpected result %+v", v)
	}
}

func TestVerifyTamperedManifest(t *testing.T) {
	f := newFixture(t)
	doc := bytes.Replace(f.manifest, []byte(`"halted": false`), []byte(`"halted": true `), 1)
	if bytes.Equal(doc, f.manifest) {
		t.Fatal("fixture did not contain the field to alter")
	}
	_, err := f.verify(doc, f.record, bytes.NewReader(f.artifact))
	wantErr(t, err, ErrManifest)
	wantErr(t, err, packages.ErrBadSignature)
}

func TestVerifyTamperedRecord(t *testing.T) {
	f := newFixture(t)
	rec := bytes.Replace(f.record, []byte("patch"), []byte("major"), 1)
	_, err := f.verify(f.manifest, rec, bytes.NewReader(f.artifact))
	wantErr(t, err, ErrRecordSig)
}

func TestVerifyWrongKeyManifestSignedByReleaseKey(t *testing.T) {
	f := newFixture(t)
	doc := f.signManifest(t, f.relKey, f.manifestBody(testNow.Add(-time.Hour), 14*24*time.Hour))
	_, err := f.verify(doc, f.record, bytes.NewReader(f.artifact))
	wantErr(t, err, ErrManifest)
	wantErr(t, err, packages.ErrUntrustedKey)
}

func TestVerifyWrongKeyRecordSignedByManifestKey(t *testing.T) {
	f := newFixture(t)
	f.recordSig = f.signRecord(t, f.manKey, f.record)
	doc := f.signManifest(t, f.manKey, f.manifestBody(testNow.Add(-time.Hour), 14*24*time.Hour))
	_, err := f.verify(doc, f.record, bytes.NewReader(f.artifact))
	wantErr(t, err, ErrRecordSig)
	wantErr(t, err, packages.ErrUntrustedKey)
}

func TestVerifyExpired(t *testing.T) {
	f := newFixture(t)
	doc := f.signManifest(t, f.manKey, f.manifestBody(testNow.Add(-15*24*time.Hour), 14*24*time.Hour))
	_, err := f.verify(doc, f.record, bytes.NewReader(f.artifact))
	wantErr(t, err, ErrExpired)
}

func TestManifestFreshnessErrorsNameTheManifest(t *testing.T) {
	f := newFixture(t)
	late := f.opts
	late.Now = func() time.Time { return testNow.Add(30 * 24 * time.Hour) }
	_, err := VerifyManifest(f.manifest, late)
	wantErr(t, err, ErrExpired)
	if strings.Contains(err.Error(), "registry") || !strings.Contains(err.Error(), "release manifest has expired") {
		t.Errorf("text: %v", err)
	}
	doc := f.signManifest(t, f.manKey, f.manifestBody(testNow, 20*24*time.Hour))
	_, err = VerifyManifest(doc, f.opts)
	wantErr(t, err, ErrNotFresh)
	if strings.Contains(err.Error(), "registry") {
		t.Errorf("text: %v", err)
	}
}

func TestVerifyTooLongLived(t *testing.T) {
	f := newFixture(t)
	long := f.signManifest(t, f.manKey, f.manifestBody(testNow.Add(-time.Hour), 16*24*time.Hour))
	_, err := f.verify(long, f.record, bytes.NewReader(f.artifact))
	wantErr(t, err, ErrNotFresh)
	edge := f.signManifest(t, f.manKey, f.manifestBody(testNow.Add(-time.Hour), 15*24*time.Hour))
	if _, err := f.verify(edge, f.record, bytes.NewReader(f.artifact)); err != nil {
		t.Fatalf("15 days must pass: %v", err)
	}
}

func TestVerifyFutureIssued(t *testing.T) {
	f := newFixture(t)
	doc := f.signManifest(t, f.manKey, f.manifestBody(testNow.Add(time.Hour), 14*24*time.Hour))
	_, err := f.verify(doc, f.record, bytes.NewReader(f.artifact))
	wantErr(t, err, ErrNotFresh)
}

func TestVerifyArtifactSHA(t *testing.T) {
	f := newFixture(t)
	bad := bytes.Repeat([]byte("x"), len(f.artifact))
	_, err := f.verify(f.manifest, f.record, bytes.NewReader(bad))
	wantErr(t, err, ErrArtifactHash)
}

func TestVerifyArtifactSizeMismatch(t *testing.T) {
	f := newFixture(t)
	for name, data := range map[string][]byte{
		"short": f.artifact[:len(f.artifact)-1],
		"long":  append(append([]byte{}, f.artifact...), 'x'),
	} {
		_, err := f.verify(f.manifest, f.record, bytes.NewReader(data))
		if !errors.Is(err, ErrArtifactSize) {
			t.Errorf("%s: got %v, want ErrArtifactSize", name, err)
		}
	}
}

// countingReader serves endless bytes and counts what was taken.
type countingReader struct{ n int64 }

func (c *countingReader) Read(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

func TestVerifyArtifactOverCap(t *testing.T) {
	f := newFixture(t)
	f.opts.MaxArtifact = int64(len(f.artifact))
	r := &countingReader{}
	_, err := f.verify(f.manifest, f.record, r)
	wantErr(t, err, ErrArtifactSize)
	if r.n > f.opts.MaxArtifact+1 {
		t.Fatalf("read %d bytes, want at most %d", r.n, f.opts.MaxArtifact+1)
	}

	// A record that claims more than the cap is refused before any read.
	f.opts.MaxArtifact = int64(len(f.artifact)) - 1
	r = &countingReader{}
	_, err = f.verify(f.manifest, f.record, r)
	wantErr(t, err, ErrArtifactSize)
	if r.n != 0 {
		t.Fatalf("read %d bytes from an artifact the record already put over the cap", r.n)
	}
}

func TestVerifyRecordVersionMismatch(t *testing.T) {
	f := newFixture(t)
	var rec Record
	if err := json.Unmarshal(f.record, &rec); err != nil {
		t.Fatal(err)
	}
	rec.Version = "v9.9.9"
	other, err := MarshalRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	f.recordSig = f.signRecord(t, f.relKey, other)
	doc := f.signManifest(t, f.manKey, f.manifestBody(testNow.Add(-time.Hour), 14*24*time.Hour))
	_, err = f.verify(doc, other, bytes.NewReader(f.artifact))
	wantErr(t, err, ErrRecordMismatch)
}

func TestVerifyNoSuchReleaseAndPlatform(t *testing.T) {
	f := newFixture(t)
	_, err := Verify(f.manifest, "v0.0.1", f.record, f.platform, strings.NewReader(""), f.opts)
	wantErr(t, err, ErrNoSuchRelease)
	_, err = Verify(f.manifest, "v1.2.3", f.record, "plan9-386", strings.NewReader(""), f.opts)
	wantErr(t, err, ErrRecordMismatch)
}

func TestVerifyReportsYanked(t *testing.T) {
	f := newFixture(t)
	m := f.manifestBody(testNow.Add(-time.Hour), 14*24*time.Hour)
	m.Yanked = []Yank{{Version: "v1.2.3", Reason: "bad migration", To: "v1.2.4"}}
	doc := f.signManifest(t, f.manKey, m)
	v, err := f.verify(doc, f.record, bytes.NewReader(f.artifact))
	if err != nil {
		t.Fatal(err)
	}
	if v.Yanked == nil || v.Yanked.To != "v1.2.4" || v.Yanked.Reason != "bad migration" {
		t.Fatalf("yank not reported: %+v", v.Yanked)
	}
}

func TestVerifyManifestOnly(t *testing.T) {
	f := newFixture(t)
	m, err := VerifyManifest(f.manifest, f.opts)
	if err != nil || len(m.Releases) != 1 {
		t.Fatalf("got %v, %v", m, err)
	}
}

func TestVerifyRefusesOtherChannel(t *testing.T) {
	f := newFixture(t)
	o := f.opts
	o.Channel = "edge" // the manifest is signed for stable
	if _, err := VerifyManifest(f.manifest, o); !errors.Is(err, ErrChannelMismatch) {
		t.Fatalf("VerifyManifest: got %v, want ErrChannelMismatch", err)
	}
	if _, err := Verify(f.manifest, "v1.2.3", f.record, f.platform, bytes.NewReader(f.artifact), o); !errors.Is(err, ErrChannelMismatch) {
		t.Fatalf("Verify: got %v, want ErrChannelMismatch", err)
	}
	for _, c := range []string{"", "beta"} {
		o.Channel = c
		if _, err := VerifyManifest(f.manifest, o); !errors.Is(err, ErrChannelMismatch) {
			t.Errorf("channel %q: got %v, want ErrChannelMismatch", c, err)
		}
	}
}

func TestVerifyChannelMatrix(t *testing.T) {
	f := newFixture(t)
	for _, signed := range Channels {
		body := f.manifestBody(testNow.Add(-time.Hour), 14*24*time.Hour)
		body.Channel = signed
		doc := f.signManifest(t, f.manKey, body)
		for _, want := range Channels {
			o := f.opts
			o.Channel = want
			_, err := VerifyManifest(doc, o)
			if signed == want && err != nil {
				t.Errorf("signed %s, options %s: %v", signed, want, err)
			}
			if signed != want && !errors.Is(err, ErrChannelMismatch) {
				t.Errorf("signed %s, options %s: got %v, want ErrChannelMismatch", signed, want, err)
			}
		}
	}
	o := f.opts
	o.Channel = "nightly"
	if _, err := VerifyManifest(f.manifest, o); !errors.Is(err, ErrChannelMismatch) {
		t.Errorf("nightly: got %v, want ErrChannelMismatch", err)
	}
}

func TestVerifyRecordAlone(t *testing.T) {
	f := newFixture(t)
	rec, err := VerifyRecord(f.record, f.recordSig, f.opts)
	if err != nil || rec.Version != "v1.2.3" {
		t.Fatalf("got %+v, %v", rec, err)
	}
	other, _ := genKey(t)
	if _, err := VerifyRecord(f.record, f.signRecord(t, other, f.record), f.opts); !errors.Is(err, ErrRecordSig) {
		t.Errorf("foreign key: got %v, want ErrRecordSig", err)
	}
	if _, err := VerifyRecord(append(f.record, ' '), f.recordSig, f.opts); !errors.Is(err, ErrRecordSig) {
		t.Errorf("changed bytes: got %v, want ErrRecordSig", err)
	}
	// A record signed by the manifest key is not a release record.
	if _, err := VerifyRecord(f.record, f.signRecord(t, f.manKey, f.record), f.opts); !errors.Is(err, ErrRecordSig) {
		t.Errorf("manifest key: got %v, want ErrRecordSig", err)
	}
	junk := []byte("not json")
	if _, err := VerifyRecord(junk, f.signRecord(t, f.relKey, junk), f.opts); !errors.Is(err, ErrRecordMismatch) {
		t.Errorf("signed junk: got %v, want ErrRecordMismatch", err)
	}
}

func TestVerifyArtifactAlone(t *testing.T) {
	f := newFixture(t)
	rec, err := VerifyRecord(f.record, f.recordSig, f.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyArtifact(rec, f.platform, bytes.NewReader(f.artifact), f.opts); err != nil {
		t.Fatal(err)
	}
	if err := VerifyArtifact(rec, "plan9-386", bytes.NewReader(f.artifact), f.opts); !errors.Is(err, ErrRecordMismatch) {
		t.Errorf("unknown platform: got %v", err)
	}
	if err := VerifyArtifact(rec, f.platform, strings.NewReader("pretend this is a binarY"), f.opts); !errors.Is(err, ErrArtifactHash) {
		t.Errorf("same size, other bytes: got %v", err)
	}
	if err := VerifyArtifact(rec, f.platform, strings.NewReader("short"), f.opts); !errors.Is(err, ErrArtifactSize) {
		t.Errorf("short: got %v", err)
	}
	small := f.opts
	small.MaxArtifact = 4
	if err := VerifyArtifact(rec, f.platform, bytes.NewReader(f.artifact), small); !errors.Is(err, ErrArtifactSize) {
		t.Errorf("over the cap: got %v", err)
	}
}

func TestValidVersion(t *testing.T) {
	for _, v := range []string{"v1.2.3", "v0.0.0", "v10.20.30", "v1.2.3-rc.1", "v1.2.3-beta", "v1.2.3-0.x-y"} {
		if !ValidVersion(v) {
			t.Errorf("%q should be valid", v)
		}
	}
	for _, v := range []string{"", "1.2.3", "v1.2", "v1.2.3+build", "v1.2.3-rc.1+b", "v1.2.3-", "v1.2.3-rc..1", "v01.2.3", "v+1.2.3", "v1.2.3-01", " v1.2.3", "v1.2.3\n", "V1.2.3"} {
		if ValidVersion(v) {
			t.Errorf("%q should be refused", v)
		}
	}
}

func TestVerifyRefusesNonCanonicalVersions(t *testing.T) {
	f := newFixture(t)
	// The caller's version: exact form only, so a yank can never be missed.
	for _, v := range []string{"1.2.3", "v1.2.3+x"} {
		if _, err := Verify(f.manifest, v, f.record, f.platform, bytes.NewReader(f.artifact), f.opts); !errors.Is(err, ErrVersion) {
			t.Errorf("Verify(%q): got %v, want ErrVersion", v, err)
		}
	}
	// A signed record whose floor is not canonical.
	var rec Record
	if err := json.Unmarshal(f.record, &rec); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*Record){
		func(r *Record) { r.Version = "v1.2.3+x" },
		func(r *Record) { r.RollbackFloor = "1.2.0" },
		func(r *Record) { r.MinFrom = "" },
	} {
		r := rec
		edit(&r)
		b, _ := MarshalRecord(r)
		if _, err := VerifyRecord(b, f.signRecord(t, f.relKey, b), f.opts); !errors.Is(err, ErrVersion) {
			t.Errorf("record %+v: got %v, want ErrVersion", r, err)
		}
	}
}

func TestRecordSDKCompatibleWithIsAdditive(t *testing.T) {
	rec := Record{Version: "v1.2.3", Kind: "patch", SDK: "0.10.0", SchemaHead: 5}
	plain, err := MarshalRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain, []byte("sdk_compatible_with")) {
		t.Fatalf("empty list must be omitted:\n%s", plain)
	}
	rec.SDKCompatibleWith = []string{"0.9.0"}
	with, _ := MarshalRecord(rec)
	var got Record
	if err := json.Unmarshal(with, &got); err != nil || len(got.SDKCompatibleWith) != 1 {
		t.Fatalf("round trip: %+v, %v", got, err)
	}
	// An older binary's view of the record (no such field) still decodes it.
	var old struct{ Version, SDK string }
	if err := json.Unmarshal(with, &old); err != nil || old.SDK != "0.10.0" {
		t.Fatalf("older reader: %+v, %v", old, err)
	}
}
