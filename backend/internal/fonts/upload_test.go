package fonts

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestValidateUpload(t *testing.T) {
	ok := append([]byte("wOF2"), make([]byte, 100)...)
	tests := []struct {
		name   string
		family string
		weight int
		style  string
		body   []byte
		want   error
	}{
		{"ok", "Brand Sans", 400, "normal", ok, nil},
		{"italic 900", "Brand-Sans_2.0", 900, "italic", ok, nil},
		{"not woff2", "Brand", 400, "normal", []byte("OTTO0000"), ErrNotWOFF2},
		{"woff1", "Brand", 400, "normal", []byte("wOFF0000"), ErrNotWOFF2},
		{"too big", "Brand", 400, "normal", append([]byte("wOF2"), make([]byte, MaxFileBytes)...), ErrTooBig},
		{"weight step", "Brand", 450, "normal", ok, ErrBadWeight},
		{"weight low", "Brand", 0, "normal", ok, ErrBadWeight},
		{"weight high", "Brand", 1000, "normal", ok, ErrBadWeight},
		{"style", "Brand", 400, "oblique", ok, ErrBadStyle},
		{"empty family", "", 400, "normal", ok, ErrBadFamily},
		{"quote in family", `Br"and`, 400, "normal", ok, ErrBadFamily},
		{"comma in family", "A, B", 400, "normal", ok, ErrBadFamily},
		{"long family", strings.Repeat("a", 61), 400, "normal", ok, ErrBadFamily},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, hash, err := ValidateUpload(tc.family, tc.weight, tc.style, bytes.NewReader(tc.body))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.want == nil && (len(hash) != 64 || !bytes.Equal(b, tc.body)) {
				t.Errorf("hash %q, bytes equal %v", hash, bytes.Equal(b, tc.body))
			}
		})
	}
}

func TestMaxSizeAccepted(t *testing.T) {
	b := append([]byte("wOF2"), make([]byte, MaxFileBytes-4)...)
	if _, _, err := ValidateUpload("Brand", 400, "normal", bytes.NewReader(b)); err != nil {
		t.Errorf("exactly 2 MB: %v", err)
	}
}
