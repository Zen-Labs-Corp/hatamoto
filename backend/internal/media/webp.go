package media

import (
	"encoding/binary"
	"io"
	"os"
)

// maxWebpChunks bounds the chunk walk so a file full of zero-length chunks
// can't spin the loop. Real WebP files have a handful of chunks.
const maxWebpChunks = 100_000

// webpFramePixels reads the true VP8/VP8L frame dimensions from a WebP file by
// SEEKING through its RIFF chunk headers (not a fixed prefix), so a large
// padding/JUNK chunk cannot push the frame chunk out of view.
//
// Returns:
//   - (pixels>0, true): the real frame area (what Go's decoder will allocate).
//   - (0, true): the file IS a WebP but no frame chunk could be parsed —
//     callers MUST reject it (fail closed) rather than decode.
//   - (0, false): not a WebP container at all — callers use the normal path.
//
// This matters because image.DecodeConfig returns the VP8X *canvas* size, which
// an extended-WebP bomb makes tiny while the real frame is enormous.
func webpFramePixels(path string) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	header := make([]byte, 12)
	if _, err := io.ReadFull(f, header); err != nil {
		return 0, false
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WEBP" {
		return 0, false
	}
	// From here the file is a WebP: never fall through to a canvas-based check.

	var off int64 = 12
	chunkHdr := make([]byte, 8)
	for range maxWebpChunks {
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			return 0, true
		}
		if _, err := io.ReadFull(f, chunkHdr); err != nil {
			return 0, true // ran off the end without a frame chunk
		}
		fourcc := string(chunkHdr[0:4])
		size := int64(binary.LittleEndian.Uint32(chunkHdr[4:8]))

		switch fourcc {
		case "VP8 ": // lossy: 14-bit w/h at payload offset 6..10
			p := make([]byte, 10)
			if _, err := io.ReadFull(f, p); err != nil {
				return 0, true
			}
			w := int64(binary.LittleEndian.Uint16(p[6:8]) & 0x3FFF)
			h := int64(binary.LittleEndian.Uint16(p[8:10]) & 0x3FFF)
			return w * h, true
		case "VP8L": // lossless: 0x2f signature then packed 14-bit (w-1),(h-1)
			p := make([]byte, 5)
			if _, err := io.ReadFull(f, p); err != nil {
				return 0, true
			}
			if p[0] != 0x2F {
				return 0, true
			}
			bits := binary.LittleEndian.Uint32(p[1:5])
			w := int64(bits&0x3FFF) + 1
			h := int64((bits>>14)&0x3FFF) + 1
			return w * h, true
		case "ANMF": // animated frame: 24-bit (w-1),(h-1) at payload offset 6..12
			p := make([]byte, 12)
			if _, err := io.ReadFull(f, p); err != nil {
				return 0, true
			}
			w := (int64(p[6]) | int64(p[7])<<8 | int64(p[8])<<16) + 1
			h := (int64(p[9]) | int64(p[10])<<8 | int64(p[11])<<16) + 1
			return w * h, true
		}
		// Skip this chunk (header + body + even-padding) and keep scanning.
		adv := 8 + size
		if size%2 == 1 {
			adv++
		}
		if adv <= 0 {
			return 0, true
		}
		off += adv
	}
	return 0, true // too many chunks: treat as unsizable -> reject
}
