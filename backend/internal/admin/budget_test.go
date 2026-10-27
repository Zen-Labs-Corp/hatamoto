package admin

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io/fs"
	"regexp"
	"testing"

	"hatamoto.dev/core/internal/budgetfile"
	"hatamoto.dev/core/web"
)

func gzipLen(b []byte) int64 {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return int64(buf.Len())
}

var adminCSSRx = regexp.MustCompile(`^admin\.[0-9a-f]+\.css$`)

// The admin stylesheet bundle, as served, stays within its baseline.
func TestBudgetAdminCSS(t *testing.T) {
	t.Parallel()

	as, err := BuildAssets(web.Admin(), web.Vendor())
	if err != nil {
		t.Fatal(err)
	}
	var n int64 = -1
	for name, f := range as.files {
		if adminCSSRx.MatchString(name) {
			n = gzipLen(f.body)
		}
	}
	if n < 0 {
		t.Fatal("no admin.<hash>.css in the built assets")
	}
	budgetfile.Check(t, "admin", "admin_css_gzip", n, budgetfile.Metric{Unit: "bytes", TolAbs: 1024})
}

// Vendored JS: each library's byte size and the total are checked against the
// baseline and against the sizes vendor.json pins.
func TestBudgetVendoredJS(t *testing.T) {
	t.Parallel()

	vfs := web.Vendor()
	raw, err := fs.ReadFile(vfs, "vendor.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Assets []struct {
			Name  string `json:"name"`
			File  string `json:"file"`
			Bytes int64  `json:"bytes"`
			Files []struct {
				Path  string `json:"path"`
				Bytes int64  `json:"bytes"`
			} `json:"files"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, a := range m.Assets {
		var got int64
		if a.File != "" {
			b, err := fs.ReadFile(vfs, a.File)
			if err != nil {
				t.Fatal(err)
			}
			got = int64(len(b))
			if got != a.Bytes {
				t.Errorf("%s: %d bytes on disk, vendor.json pins %d", a.File, got, a.Bytes)
			}
		}
		for _, f := range a.Files {
			b, err := fs.ReadFile(vfs, f.Path)
			if err != nil {
				t.Fatal(err)
			}
			if int64(len(b)) != f.Bytes {
				t.Errorf("%s: %d bytes on disk, vendor.json pins %d", f.Path, len(b), f.Bytes)
			}
			got += int64(len(b))
		}
		total += got
		budgetfile.Check(t, "admin", "vendor_"+a.Name+"_bytes", got, budgetfile.Metric{Unit: "bytes"})
	}
	budgetfile.Check(t, "admin", "vendor_total_bytes", total, budgetfile.Metric{Unit: "bytes"})
}
