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

		self = "/proc/self/cgroup"
		cg   = "/sys/fs/cgroup"
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
		{"systemd v2 nested", noEnv, map[string]string{
			self: "0::/system.slice/hatamoto-api.service\n",
			cg + "/system.slice/hatamoto-api.service/memory.max": "536870912\n",
			cg + "/system.slice/memory.max":                      "max\n",
		}, 536870912 / 100 * 88, true},
		{"v2 parent slice tighter than the service", noEnv, map[string]string{
			self: "0::/user.slice/app.service\n",
			cg + "/user.slice/app.service/memory.max": "max",
			cg + "/user.slice/memory.max":             "1000000000",
		}, 880000000, true},
		{"v2 service tighter than the parent", noEnv, map[string]string{
			self:                   "0::/a/b\n",
			cg + "/a/b/memory.max": "1000000000",
			cg + "/a/memory.max":   "2000000000",
		}, 880000000, true},
		{"v2 nested without limits", noEnv, map[string]string{
			self: "0::/system.slice/x.service\n",
			cg + "/system.slice/x.service/memory.max": "max",
		}, 0, false},
		{"container root path", noEnv, map[string]string{self: "0::/\n", v2: "1000000000"}, 880000000, true},
		{"v1 nested memory line", noEnv, map[string]string{
			self: "12:cpu,cpuacct:/x\n4:memory:/system.slice/a.service\n1:name=systemd:/system.slice/a.service\n",
			cg + "/memory/system.slice/a.service/memory.limit_in_bytes": "9223372036854771712",
			cg + "/memory/system.slice/memory.limit_in_bytes":           "1000000000",
		}, 880000000, true},
		{"v1 container root", noEnv, map[string]string{self: "4:memory:/\n", v1: "1000000000"}, 880000000, true},
		{"hybrid prefers the v1 memory line", noEnv, map[string]string{
			self:                                     "4:memory:/s/a\n0::/s/a\n",
			cg + "/memory/s/a/memory.limit_in_bytes": "1000000000",
			cg + "/s/a/memory.max":                   "1",
		}, 880000000, true},
		{"garbage proc file falls back to the root", noEnv, map[string]string{self: "???", v2: "1000000000"}, 880000000, true},
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
