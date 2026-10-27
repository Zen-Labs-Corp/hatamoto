//go:build integration

package app

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type superviseRun struct {
	base string
	data string
	cmd  *exec.Cmd
	logs *lockedBuilder
	done chan error
}

// lockedBuilder collects child output from exec's copying goroutines.
type lockedBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuilder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuilder) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// startSupervise builds the binary and runs `hatamoto supervise` with only
// DATABASE_URL, a data dir and a free port in its environment. It returns once
// the child serving under it answers /ready.
func startSupervise(t *testing.T, dsn string, extraEnv ...string) *superviseRun {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "hatamoto")
	build := exec.Command("go", "build", "-o", bin, "../cmd/hatamoto")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	r := &superviseRun{data: filepath.Join(dir, "data"), logs: &lockedBuilder{}, done: make(chan error, 1)}
	r.cmd = exec.Command(bin, "supervise")
	r.cmd.Dir = dir
	r.cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + dir,
		"DATABASE_URL=" + dsn, "HATAMOTO_DATA=" + r.data,
		fmt.Sprintf("API_PORT=%d", port), "API_HOST=127.0.0.1",
	}, extraEnv...)
	r.cmd.Stdout, r.cmd.Stderr = r.logs, r.logs
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { r.done <- r.cmd.Wait() }()
	t.Cleanup(func() {
		select {
		case <-r.done:
		default:
			_ = r.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-r.done:
			case <-time.After(15 * time.Second):
				_ = r.cmd.Process.Kill()
			}
		}
		if t.Failed() {
			t.Logf("supervisor log:\n%s", r.logs.String())
		}
	})
	r.base = fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(r.base + "/ready"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return r
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("child never became ready under supervise:\n%s", r.logs.String())
	return nil
}

var smokeAddrRe = regexp.MustCompile(`smoke_addr=(\S+)`)

// ISC-12: the real serve command, run by the real supervisor, reaches /ready;
// its smoke endpoint passes with the token from state.json; a SIGTERM stops
// both and the supervisor exits 0.
func TestSupervise_ServeReachesReadyAndSmoke_Integration(t *testing.T) {
	r := startSupervise(t, scratchDSN(t))

	m := smokeAddrRe.FindStringSubmatch(r.logs.String())
	if m == nil {
		t.Fatalf("supervisor log has no smoke_addr:\n%s", r.logs.String())
	}
	statePath := filepath.Join(r.data, "releases", "state.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		SmokeToken string `json:"smoke_token"`
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.SmokeToken == "" {
		t.Fatalf("state.json: %v %s", err, raw)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://"+m[1]+"/smoke", nil)
	req.Header.Set("Authorization", "Bearer "+st.SmokeToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var res struct {
		Pass   bool `json:"pass"`
		Checks []struct {
			Name   string `json:"name"`
			Status int    `json:"status"`
		} `json:"checks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !res.Pass || len(res.Checks) == 0 {
		t.Fatalf("smoke: HTTP %d %+v", resp.StatusCode, res)
	}
	for _, c := range res.Checks {
		if c.Name == "GET /" && c.Status != 200 {
			t.Errorf("GET / = %d on a fresh install", c.Status)
		}
	}

	// The supervisor probes the running child once and keeps that as the
	// baseline for the next switch.
	deadline := time.Now().Add(20 * time.Second)
	for {
		raw, _ := os.ReadFile(statePath)
		var s struct {
			Baseline map[string]int `json:"smoke_baseline"`
		}
		_ = json.Unmarshal(raw, &s)
		if s.Baseline["GET /"] == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no smoke baseline recorded: %s", raw)
		}
		time.Sleep(200 * time.Millisecond)
	}

	if err := r.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-r.done:
		if err != nil {
			t.Fatalf("supervisor exit: %v\n%s", err, r.logs.String())
		}
	case <-time.After(45 * time.Second):
		t.Fatal("supervisor did not exit after SIGTERM")
	}
	if resp, err := http.Get(r.base + "/health"); err == nil {
		resp.Body.Close()
		t.Error("child still serving after the supervisor exited")
	}
	for _, w := range []string{"panic", "FATAL"} {
		if strings.Contains(r.logs.String(), w) {
			t.Errorf("log contains %q:\n%s", w, r.logs.String())
		}
	}
}
