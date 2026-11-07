package form

import (
	"testing"

	"hatamoto.dev/core/internal/jsonx"
)

func TestFileRefString(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		size int64
		want string
	}{
		{0, "a.pdf (0 B)"}, {1023, "a.pdf (1023 B)"}, {240 * 1024, "a.pdf (240 KB)"}, {5*1024*1024 + 512*1024, "a.pdf (5.5 MB)"},
	} {
		if got := (FileRef{Name: "a.pdf", Size: tc.size}).String(); got != tc.want {
			t.Errorf("size %d = %q, want %q", tc.size, got, tc.want)
		}
	}
}

func TestFileRefFromAnyJSONRoundTrip(t *testing.T) {
	t.Parallel()
	in := FileRef{ID: "0123456789abcdef0123456789abcdef", Name: "cv.pdf", Size: 2048, Type: "application/pdf"}
	raw, err := jsonx.Marshal(map[string]any{"cv": in})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := jsonx.Unmarshal(raw, &back, jsonx.StoredLoose); err != nil {
		t.Fatal(err)
	}
	got, ok := FileRefFromAny(back["cv"])
	if !ok || got != in {
		t.Fatalf("got %+v ok=%v, want %+v", got, ok, in)
	}
	for _, bad := range []any{nil, "x", 3, map[string]any{"name": "x"}, []any{}} {
		if _, ok := FileRefFromAny(bad); ok {
			t.Errorf("FileRefFromAny(%v) ok", bad)
		}
	}
}

func TestFileFieldAccessors(t *testing.T) {
	t.Parallel()
	big := 99
	f := FormField{}
	if f.FileMaxMB() != DefaultFileMaxMB || f.FileAccept() != "" {
		t.Fatalf("zero field: %d %q", f.FileMaxMB(), f.FileAccept())
	}
	f.Validation = &FieldValidation{Accept: []string{"pdf", "docx"}, MaxMB: &big}
	if f.FileAccept() != ".pdf,.docx" || f.FileMaxMB() != MaxFileUploadMB {
		t.Fatalf("got %q %d", f.FileAccept(), f.FileMaxMB())
	}
}
