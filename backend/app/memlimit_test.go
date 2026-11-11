package app

import (
	"errors"
	"os"
	"testing"
)

func TestDefaultMemLimit(t *testing.T) {
	t.Parallel()

	const (
		v2 = "/sys/fs/cgroup/memory.max"
		v1 = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	)
	files := func(m map[string]string) func(string) ([]byte, error) {
		return func(p string) ([]byte, error) {
			if s, ok := m[p]; ok {
				return []byte(s), nil
			}
			return nil, os.ErrNotExist
		}
	}
	noEnv := func(string) string { return "" }
	tests := []struct {
		name  string
		env   func(string) string
		files map[string]string
		want  int64
		ok    bool
	}{
		{"operator GOMEMLIMIT wins", func(k string) string {
			if k == "GOMEMLIMIT" {
				return "512MiB"
			}
			return ""
		}, map[string]string{v2: "1000000000\n"}, 0, false},
		{"v2 number", noEnv, map[string]string{v2: "1000000000\n"}, 880000000, true},
		{"v2 max", noEnv, map[string]string{v2: "max\n"}, 0, false},
		{"v2 max does not fall back to v1", noEnv, map[string]string{v2: "max\n", v1: "1000000000"}, 0, false},
		{"v1 fallback", noEnv, map[string]string{v1: "1000000000\n"}, 880000000, true},
		{"v1 huge", noEnv, map[string]string{v1: "9223372036854771712\n"}, 0, false},
		{"missing files", noEnv, map[string]string{}, 0, false},
		{"garbage", noEnv, map[string]string{v2: "lots"}, 0, false},
		{"zero", noEnv, map[string]string{v2: "0"}, 0, false},
	}
	for _, tc := range tests {
		got, ok := defaultMemLimit(tc.env, files(tc.files))
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: got (%d, %v), want (%d, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := defaultMemLimit(noEnv, func(string) ([]byte, error) { return nil, errors.New("boom") }); ok {
		t.Error("read error must leave the limit unset")
	}
}
