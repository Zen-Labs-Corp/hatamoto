package twofactor

import (
	"image/color"
	"strconv"
	"strings"
	"testing"

	"github.com/boombuler/barcode/qr"
)

func TestQRSVG(t *testing.T) {
	uri := ProvisioningURI(`Site "<b>" & co`, "ana@example.com", []byte("12345678901234567890"))
	svg, err := QRSVG(uri)
	if err != nil {
		t.Fatal(err)
	}
	s := string(svg)
	for _, bad := range []string{"<script", "href", "xlink", "http://", "https://", "javascript:", "onload", "<image", "<style"} {
		if strings.Contains(strings.ToLower(s), bad) {
			t.Errorf("svg contains %q", bad)
		}
	}
	if !strings.HasPrefix(s, "<svg ") || !strings.Contains(s, `role="img"`) || !strings.Contains(s, "<title") {
		t.Errorf("svg lacks its accessible shell: %.120s", s)
	}
	if strings.Count(s, "<path") != 1 {
		t.Error("want one path")
	}
	// Nothing from the content leaks into the markup.
	if strings.Contains(s, "ana@example.com") || strings.Contains(s, "Site") {
		t.Error("content text appears in the svg")
	}
}

// The painted modules equal the matrix the encoder produced, offset by the quiet zone.
func TestQRSVGMatchesTheMatrix(t *testing.T) {
	const content = "otpauth://totp/x?secret=ABC"
	svg, err := QRSVG(content)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := qr.Encode(content, qr.M, qr.Auto)
	n := code.Bounds().Dx()
	dark := 0
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if code.At(x, y) == colorBlack {
				dark++
				if !strings.Contains(string(svg), "M"+itoa(x+4)+" "+itoa(y+4)+"h1v1h-1z") {
					t.Fatalf("module %d,%d missing", x, y)
				}
			}
		}
	}
	if got := strings.Count(string(svg), "h1v1h-1z"); got != dark {
		t.Fatalf("painted %d modules, matrix has %d", got, dark)
	}
	if !strings.Contains(string(svg), `viewBox="0 0 `+itoa(n+8)+` `+itoa(n+8)+`"`) {
		t.Error("viewBox does not include the quiet zone")
	}
}

func TestQRSVGEmpty(t *testing.T) {
	if _, err := QRSVG(""); err == nil {
		t.Fatal("empty content accepted")
	}
}

var colorBlack = color.Black

func itoa(n int) string { return strconv.Itoa(n) }
