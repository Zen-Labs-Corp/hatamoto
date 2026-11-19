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

func TestEnforcePixelCap_RejectsCanvasAndLaterFrameBombs(t *testing.T) {
	svc := newTestService(t, Config{MaxPixels: defaultMaxPixels})
	dir := filepath.Join(svc.config.UploadDir, legacyMediaTenantDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	canvasBomb := appendChunk(buildVP8XWebP(0x10, 46340, 46340), "ALPH", make([]byte, 4))
	canvasBomb = appendChunk(canvasBomb, "VP8 ", vp8Payload(1, 1))

	laterFrame := appendChunk(buildVP8XWebP(0x02, 1, 1), "ANMF", make([]byte, 16))
	laterFrame = appendChunk(laterFrame, "VP8L", vp8lPayload(16384, 16384))

	smallL := appendChunk([]byte("RIFF\x00\x00\x00\x00WEBP"), "VP8L", vp8lPayload(64, 64))

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"canvas with alpha and tiny frame", canvasBomb, ErrImageTooLarge},
		{"animation chunk before big frame", laterFrame, ErrImageTooLarge},
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
