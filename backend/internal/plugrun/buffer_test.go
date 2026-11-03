package plugrun

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckPut(t *testing.T) {
	tests := []struct {
		name                        string
		key                         string
		valLen, keys, bytes, oldLen int
		want                        error
	}{
		{"ok new", "a", 10, 0, 0, -1, nil},
		{"empty key", "", 1, 0, 0, -1, ErrInvalid},
		{"long key", strings.Repeat("k", MaxBufferKeyLen+1), 1, 0, 0, -1, ErrInvalid},
		{"max key", strings.Repeat("k", MaxBufferKeyLen), 1, 0, 0, -1, nil},
		{"nul key", "a\x00b", 1, 0, 0, -1, ErrInvalid},
		{"bad utf8", "a\xffb", 1, 0, 0, -1, ErrInvalid},
		{"value too big", "a", MaxBufferValue + 1, 0, 0, -1, ErrQuota},
		{"key quota", "a", 1, MaxBufferKeys, 0, -1, ErrQuota},
		{"replace at key quota", "a", 1, MaxBufferKeys, 5, 1, nil},
		{"byte quota", "a", 10, 1, MaxBufferBytes - 9, -1, ErrQuota},
		{"byte quota exact", "a", 10, 1, MaxBufferBytes - 10, -1, nil},
		{"replace shrinks", "a", 1, 1, MaxBufferBytes, MaxBufferValue, nil},
		{"replace grows past", "a", 100, 1, MaxBufferBytes, 10, ErrQuota},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkPut(tc.key, tc.valLen, tc.keys, tc.bytes, tc.oldLen)
			if !errors.Is(got, tc.want) && !(tc.want == nil && got == nil) {
				t.Fatalf("checkPut = %v, want %v", got, tc.want)
			}
		})
	}
}
