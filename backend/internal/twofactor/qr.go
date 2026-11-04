package twofactor

import (
	"errors"
	"html/template"
	"image/color"
	"strconv"
	"strings"

	"github.com/boombuler/barcode/qr"
)

const qrQuietZone = 4 // modules of white around the code, as the QR spec asks

// QRSVG renders content as an inline SVG QR code (error correction M). The
// SVG has one path of unit squares, a fixed title and no external references
// or scripts; content only ever becomes modules, never markup.
func QRSVG(content string) (template.HTML, error) {
	if content == "" {
		return "", errors.New("twofactor: empty QR content")
	}
	code, err := qr.Encode(content, qr.M, qr.Auto)
	if err != nil {
		return "", err
	}
	b := code.Bounds()
	n := b.Dx()
	size := strconv.Itoa(n + 2*qrQuietZone)

	var path strings.Builder
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if code.At(b.Min.X+x, b.Min.Y+y) == color.Black {
				path.WriteString("M" + strconv.Itoa(x+qrQuietZone) + " " + strconv.Itoa(y+qrQuietZone) + "h1v1h-1z")
			}
		}
	}
	return template.HTML(`<svg viewBox="0 0 ` + size + ` ` + size + `" role="img" aria-labelledby="qr-title" shape-rendering="crispEdges">` +
		`<title id="qr-title">QR code for your authenticator app</title>` +
		`<rect width="` + size + `" height="` + size + `" fill="#fff"/>` +
		`<path d="` + path.String() + `" fill="#000"/></svg>`), nil
}
