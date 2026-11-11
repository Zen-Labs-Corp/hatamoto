package backup

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Byte pins for the backup files: manifest.json, the <id>.json sidecar and
// retention.json, as the previous encoder wrote them. Only values without
// HTML characters are pinned byte for byte; an empty list or map may later be
// written as [] or {} instead of null, so those cases are pinned by decoding.

const (
	headManifestNulls = "{\n  \"format\": \"hatamoto-backup\",\n  \"version\": 1,\n  \"created\": \"2026-10-25T10:15:00Z\",\n  \"kind\": \"manual\",\n  \"hatamoto_version\": \"v1.2.3\",\n  \"server_major\": 16,\n  \"pg_dump\": \"16.2\",\n  \"parts\": [\n    {\n      \"name\": \"core.dump\",\n      \"size\": 4,\n      \"sha256\": \"0d45f5fd462b8c70bffb10021ac1bcff3f58f29b1faf7568595095427d42812c\"\n    }\n  ],\n  \"plugins\": null,\n  \"content_rows\": null,\n  \"media_files\": 2,\n  \"media_bytes\": 6\n}"
	headManifestFull  = "{\n  \"format\": \"hatamoto-backup\",\n  \"version\": 1,\n  \"created\": \"2026-10-25T10:15:00Z\",\n  \"kind\": \"manual\",\n  \"target\": \"db-x\",\n  \"hatamoto_version\": \"v1.2.3\",\n  \"server_major\": 16,\n  \"pg_dump\": \"16.2\",\n  \"parts\": [\n    {\n      \"name\": \"core.dump\",\n      \"size\": 4,\n      \"sha256\": \"0d45f5fd462b8c70bffb10021ac1bcff3f58f29b1faf7568595095427d42812c\"\n    }\n  ],\n  \"plugins\": [\n    \"forms\"\n  ],\n  \"content_rows\": {\n    \"page\": 1,\n    \"post\": 3\n  },\n  \"media_files\": 2,\n  \"media_bytes\": 6,\n  \"media_missing\": 1,\n  \"trust\": {\n    \"release\": [\n      \"r1\"\n    ],\n    \"manifest\": [\n      \"m1\"\n    ]\n  },\n  \"config_path\": \"/etc/hatamoto/hatamoto.env\"\n}"
	headSidecarFull   = "{\n  \"id\": \"20261025T101500Z-manual\",\n  \"kind\": \"manual\",\n  \"target\": \"db-x\",\n  \"state\": \"ok\",\n  \"step\": \"done\",\n  \"percent\": 100,\n  \"error\": \"e-f\",\n  \"created\": \"2026-10-25T10:15:00Z\",\n  \"size\": 42,\n  \"parts\": [\n    \"core.dump\"\n  ],\n  \"plugins\": [\n    \"forms\"\n  ],\n  \"media_missing\": 2,\n  \"sha256\": \"abc\",\n  \"offsite\": \"failed\",\n  \"offsite_error\": \"boom\"\n}"
	headSidecarMin    = "{\n  \"id\": \"20261025T101500Z-manual\",\n  \"kind\": \"manual\",\n  \"state\": \"ok\",\n  \"percent\": 0,\n  \"created\": \"2026-10-25T10:15:00Z\"\n}"
	headRetention     = "{\n  \"manual\": 3,\n  \"scheduled\": 9\n}\n"
	headPinID         = "20261025T101500Z-manual"
)

var pinCreated = time.Date(2026, 10, 25, 10, 15, 0, 0, time.UTC)

// tarWith writes a tar of the named entries, in order.
func tarWith(t *testing.T, names []string, bodies map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "b.tar")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o600, Size: int64(len(bodies[n]))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(bodies[n]); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// manifestOf runs mf through writeArchive and returns the manifest entry.
func manifestOf(t *testing.T, mf manifest) []byte {
	t.Helper()
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, coreName), []byte("core"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := describe(work, coreName)
	if err != nil {
		t.Fatal(err)
	}
	mf.Parts = []part{p}
	if _, _, err := writeArchive(st, headPinID, work, mf.Parts, mf); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(st.archive(headPinID))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == manifestName {
			b, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
	}
}

func pinManifest() manifest {
	return manifest{
		Format: manifestFormat, Version: manifestVersion, Created: pinCreated, Kind: KindManual,
		HatamotoVersion: "v1.2.3", ServerMajor: 16, PgDump: "16.2", MediaFiles: 2, MediaBytes: 6,
	}
}

// A manifest written with null plugins and content_rows scans and restores.
func TestHeadManifestRestores(t *testing.T) {
	path := tarWith(t, []string{coreName, manifestName}, map[string][]byte{coreName: []byte("core"), manifestName: []byte(headManifestNulls)})
	sc, err := scanArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if sc.mf.Format != manifestFormat || sc.mf.Version != 1 || sc.mf.Kind != KindManual || !sc.mf.Created.Equal(pinCreated) ||
		sc.mf.HatamotoVersion != "v1.2.3" || sc.mf.ServerMajor != 16 || sc.mf.PgDump != "16.2" ||
		len(sc.mf.Parts) != 1 || sc.mf.Parts[0].Name != coreName || sc.mf.Parts[0].Size != 4 ||
		len(sc.mf.Plugins) != 0 || len(sc.mf.ContentRows) != 0 || sc.mf.MediaFiles != 2 || sc.mf.MediaBytes != 6 {
		t.Fatalf("manifest = %+v", sc.mf)
	}
	r := newRestoreRig(t)
	plan, err := r.dry(t, path)
	if err != nil {
		t.Fatalf("dry-run restore: %v", err)
	}
	if plan == nil {
		t.Fatal("no plan")
	}
}

func TestHeadFullManifestScans(t *testing.T) {
	path := tarWith(t, []string{manifestName}, map[string][]byte{manifestName: []byte(headManifestFull)})
	sc, err := scanArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	mf := sc.mf
	if !reflect.DeepEqual(mf.Plugins, []string{"forms"}) || !reflect.DeepEqual(mf.ContentRows, map[string]int64{"post": 3, "page": 1}) ||
		mf.MediaMissing != 1 || mf.Target != "db-x" || mf.ConfigPath != "/etc/hatamoto/hatamoto.env" ||
		mf.Trust == nil || !reflect.DeepEqual(mf.Trust.Release, []string{"r1"}) || !reflect.DeepEqual(mf.Trust.Manifest, []string{"m1"}) {
		t.Fatalf("manifest = %+v", mf)
	}
}

func TestManifestWriteBytes(t *testing.T) {
	full := pinManifest()
	full.Target = "db-x"
	full.Plugins = []string{"forms"}
	full.ContentRows = map[string]int64{"post": 3, "page": 1}
	full.MediaMissing = 1
	full.Trust = &TrustIDs{Release: []string{"r1"}, Manifest: []string{"m1"}}
	full.ConfigPath = "/etc/hatamoto/hatamoto.env"
	if got := string(manifestOf(t, full)); got != headManifestFull {
		t.Fatalf("manifest bytes changed:\n got %q\nwant %q", got, headManifestFull)
	}
	// With no plugins or rows the encoder may write null or an empty value;
	// only the decoded result is pinned.
	path := tarWith(t, []string{manifestName}, map[string][]byte{manifestName: manifestOf(t, pinManifest())})
	sc, err := scanArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(sc.mf.Plugins) != 0 || len(sc.mf.ContentRows) != 0 || len(sc.mf.Parts) != 1 || sc.mf.HatamotoVersion != "v1.2.3" {
		t.Fatalf("manifest = %+v", sc.mf)
	}
}

func pinInfo() Info {
	return Info{ID: headPinID, Kind: KindManual, Target: "db-x", State: StateOK, Step: "done", Percent: 100, Error: "e-f", Created: pinCreated,
		Size: 42, Parts: []string{"core.dump"}, Plugins: []string{"forms"}, MediaMissing: 2, SHA256: "abc", Offsite: "failed", OffsiteError: "boom"}
}

func TestSidecarInfoHeadBytesLoad(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		body string
		want Info
	}{
		{"full", headSidecarFull, pinInfo()},
		{"minimal", headSidecarMin, Info{ID: headPinID, Kind: KindManual, State: StateOK, Created: pinCreated}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := os.WriteFile(st.sidecar(headPinID), []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := st.Get(headPinID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("info = %+v\nwant   %+v", got, c.want)
			}
		})
	}
}

func TestSidecarWriteBytes(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		in   Info
		want string
	}{
		{"full", pinInfo(), headSidecarFull},
		{"minimal", Info{ID: headPinID, Kind: KindManual, State: StateOK, Created: pinCreated}, headSidecarMin},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := st.Write(c.in); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(st.sidecar(headPinID))
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != c.want {
				t.Fatalf("sidecar bytes changed:\n got %q\nwant %q", b, c.want)
			}
		})
	}
}

// The retention file is read leniently: its failure is swallowed, so a stricter
// reader would silently reset the counts.
func TestRetentionDuplicateKeyStillApplies(t *testing.T) {
	for _, c := range []struct {
		body string
		want map[string]int
	}{
		{`{"manual":3,"manual":4}`, map[string]int{"manual": 4}},
		{`{"Manual":3}`, map[string]int{}},
		{`{"manual":3.0,"pre_update":"x","scheduled":99}`, map[string]int{"manual": 3}},
		{`{"manual":3,"unknown":{"a":1,"a":2}}`, map[string]int{"manual": 3}},
		{`[1]`, map[string]int{}},
	} {
		dir := t.TempDir()
		writeRetention(t, dir, c.body)
		if got := readRetentionFile(dir); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: counts = %v, want %v", c.body, got, c.want)
		}
	}
}

func TestRetentionWriteBytes(t *testing.T) {
	dir := t.TempDir()
	if err := SaveRetention(dir, map[Kind]int{KindManual: 3, KindScheduled: 9}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, RetentionFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != headRetention {
		t.Fatalf("retention.json = %q, want %q", b, headRetention)
	}
}

// Roles round-trip through the manifest; without them the key is omitted, so
// older readers and the byte pins above are unaffected.
func TestManifestRolesRoundTrip(t *testing.T) {
	if strings.Contains(string(manifestOf(t, pinManifest())), "roles") {
		t.Fatal("roles key written for a manifest without roles")
	}
	mf := pinManifest()
	mf.Roles = []string{"hatamoto_role_admin_abcd1234", "hm_p_abcd1234_forms"}
	path := tarWith(t, []string{manifestName}, map[string][]byte{manifestName: manifestOf(t, mf)})
	sc, err := scanArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sc.mf.Roles, mf.Roles) {
		t.Fatalf("roles = %v", sc.mf.Roles)
	}
}
