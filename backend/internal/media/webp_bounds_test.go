package media

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildVP8XWebP returns a RIFF/WEBP header plus a VP8X chunk declaring the
// given canvas and flag byte. Callers append further chunks.
func buildVP8XWebP(flags byte, canvasW, canvasH int) []byte {
	payload := make([]byte, 10)
	payload[0] = flags
	w, h := uint32(canvasW-1), uint32(canvasH-1)
	payload[4], payload[5], payload[6] = byte(w), byte(w>>8), byte(w>>16)
	payload[7], payload[8], payload[9] = byte(h), byte(h>>8), byte(h>>16)
	b := []byte("RIFF\x00\x00\x00\x00WEBP")
	return appendChunk(b, "VP8X", payload)
}

// vp8Payload and vp8lPayload carry only the size fields the parser reads.
func vp8Payload(w, h int) []byte {
	p := make([]byte, 10)
	binary.LittleEndian.PutUint16(p[6:8], uint16(w&0x3FFF))
	binary.LittleEndian.PutUint16(p[8:10], uint16(h&0x3FFF))
	return p
}

func vp8lPayload(w, h int) []byte {
	p := make([]byte, 5)
	p[0] = 0x2F
	binary.LittleEndian.PutUint32(p[1:5], uint32(w-1)|uint32(h-1)<<14)
	return p
}

// anmfPayload carries the 16-byte ANMF header with the given frame size.
func anmfPayload(w, h int) []byte {
	p := make([]byte, 16)
	fw, fh := uint32(w-1), uint32(h-1)
	p[6], p[7], p[8] = byte(fw), byte(fw>>8), byte(fw>>16)
	p[9], p[10], p[11] = byte(fh), byte(fh>>8), byte(fh>>16)
	return p
}

func TestEnforcePixelCap_WebPCanvasAndFrameBounds(t *testing.T) {
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels})
	dir := filepath.Join(svc.config.UploadDir, legacyMediaTenantDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	canvasBomb := appendChunk(buildVP8XWebP(0x10, 46340, 46340), "ALPH", make([]byte, 4))
	canvasBomb = appendChunk(canvasBomb, "VP8 ", vp8Payload(1, 1))

	laterFrame := appendChunk(buildVP8XWebP(0x00, 1, 1), "VP8 ", vp8Payload(1, 1))
	laterFrame = appendChunk(laterFrame, "VP8L", vp8lPayload(16384, 16384))

	animOK := appendChunk(buildVP8XWebP(0x02, 64, 64), "ANMF", anmfPayload(32, 32))
	animOK = appendChunk(animOK, "ANMF", anmfPayload(64, 64))

	animBomb := appendChunk(buildVP8XWebP(0x02, 1, 1), "ANMF", anmfPayload(16384, 16384))

	animCanvasBomb := appendChunk(buildVP8XWebP(0x02, 46340, 46340), "ANMF", anmfPayload(1, 1))

	canvasOnly := buildVP8XWebP(0x02, 64, 64)

	alphaLossy := appendChunk(buildVP8XWebP(0x10, 64, 64), "ALPH", make([]byte, 4))
	alphaLossy = appendChunk(alphaLossy, "VP8 ", vp8Payload(64, 64))

	smallL := appendChunk([]byte("RIFF\x00\x00\x00\x00WEBP"), "VP8L", vp8lPayload(64, 64))

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"canvas with alpha and tiny frame", canvasBomb, ErrImageTooLarge},
		{"small frame before big later frame", laterFrame, ErrImageTooLarge},
		{"animation within the cap", animOK, nil},
		{"animation frame over the cap", animBomb, ErrImageTooLarge},
		{"animation canvas over the cap", animCanvasBomb, ErrImageTooLarge},
		{"canvas without any frame", canvasOnly, ErrImageTooLarge},
		{"canvas with alpha and lossy frame", alphaLossy, nil},
		{"plain lossy", buildVP8WebP(t, 64, 64), nil},
		{"plain lossless", smallL, nil},
	}
	for i, tc := range cases {
		p := filepath.Join(dir, string(rune('a'+i))+".webp")
		if err := os.WriteFile(p, tc.data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := svc.enforcePixelCap(p); err != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}
