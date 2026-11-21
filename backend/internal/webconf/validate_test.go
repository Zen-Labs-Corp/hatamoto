package webconf

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// validateInputs are the two layouts, with the export folder under dir.
func validateInputs(dir string) map[string]Input {
	root := filepath.Join(dir, "public")
	same, live := sameHostInput(), liveOriginInput()
	same.Root, live.Root = root, root
	return map[string]Input{"same host": same, "live origin": live}
}

// run executes a validator that only reads config: no network, no port bound.
func run(t *testing.T, env []string, name string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("%s %v: %v\n%s", name, args, err, out)
	}
}

func TestCaddyValidate(t *testing.T) {
	t.Parallel()
	caddy, err := exec.LookPath("caddy")
	if err != nil {
		t.Skip("caddy not on PATH")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, in := range validateInputs(dir) {
		out, err := Generate(in)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "Caddyfile")
		if err := os.WriteFile(file, []byte(out.Caddy), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Log(name)
		run(t, []string{"XDG_DATA_HOME=" + dir, "XDG_CONFIG_HOME=" + dir}, caddy, "validate", "--config", file, "--adapter", "caddyfile")
	}
}

func TestNginxConfig(t *testing.T) {
	t.Parallel()
	nginx, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx not on PATH")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, in := range validateInputs(dir) {
		out, err := Generate(in)
		if err != nil {
			t.Fatal(err)
		}
		conf := "pid " + dir + "/nginx.pid;\nerror_log " + dir + "/error.log;\nevents {}\nhttp {\n" +
			"access_log off;\n" +
			"client_body_temp_path " + dir + "/body;\nproxy_temp_path " + dir + "/proxy;\n" +
			"fastcgi_temp_path " + dir + "/fastcgi;\nuwsgi_temp_path " + dir + "/uwsgi;\nscgi_temp_path " + dir + "/scgi;\n" +
			out.Nginx + "}\n"
		file := filepath.Join(dir, "nginx.conf")
		if err := os.WriteFile(file, []byte(conf), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Log(name)
		run(t, nil, nginx, "-t", "-p", dir, "-c", file, "-e", dir+"/error.log")
	}
}
