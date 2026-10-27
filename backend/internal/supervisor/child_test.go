package supervisor

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	stubOnce sync.Once
	stubBin  string
	stubErr  error
)

// buildStub compiles testdata/stub once per test binary run.
func buildStub(t *testing.T) string {
	t.Helper()
	stubOnce.Do(func() {
		dir, err := os.MkdirTemp("", "supstub")
		if err != nil {
			stubErr = err
			return
		}
		stubBin = filepath.Join(dir, "stub")
		out, err := exec.Command("go", "build", "-o", stubBin, "./testdata/stub").CombinedOutput()
		if err != nil {
			stubErr = &buildError{string(out), err}
		}
	})
	if stubErr != nil {
		t.Fatalf("build stub: %v", stubErr)
	}
	return stubBin
}

type buildError struct {
	out string
	err error
}

func (e *buildError) Error() string { return e.err.Error() + ": " + e.out }

// installStub copies the stub to <dir>/hatamoto with a behavior file and
// returns its path.
func installStub(t *testing.T, dir, behavior string) string {
	t.Helper()
	src, err := os.ReadFile(buildStub(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hatamoto")
	if err := os.WriteFile(path, src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "behavior"), []byte(behavior), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func stubEnv(t *testing.T) (env []string, port string) {
	t.Helper()
	addr, err := FreeLoopbackAddr()
	if err != nil {
		t.Fatal(err)
	}
	port = addr[strings.LastIndex(addr, ":")+1:]
	smoke, err := FreeLoopbackAddr()
	if err != nil {
		t.Fatal(err)
	}
	env = ChildEnv(append(os.Environ(), "API_PORT="+port), t.TempDir(), smoke, "tok")
	return env, port
}

func waitHTTP(t *testing.T, url string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return string(b)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never answered 200", url)
	return ""
}

func TestChildEnv(t *testing.T) {
	base := []string{"PATH=/bin", "HATAMOTO_SMOKE_ADDR=old", "HATAMOTO_SMOKE_TOKEN=old", "HATAMOTO_SMOKE_X=1", "HATAMOTO_DATA=./rel", "KEEP=1"}
	got := ChildEnv(base, "/abs", "127.0.0.1:9", "tok")
	want := map[string]string{"PATH": "/bin", "KEEP": "1", "HATAMOTO_DATA": "/abs", "HATAMOTO_SMOKE_ADDR": "127.0.0.1:9", "HATAMOTO_SMOKE_TOKEN": "tok"}
	if len(got) != len(want) {
		t.Fatalf("env = %v", got)
	}
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		if want[k] != v {
			t.Errorf("%s = %q, want %q", k, v, want[k])
		}
	}
	if len(base) != 6 || base[1] != "HATAMOTO_SMOKE_ADDR=old" {
		t.Error("input slice modified")
	}
}

func TestNewTokenAndFreeAddr(t *testing.T) {
	a, _ := NewToken()
	b, _ := NewToken()
	if len(a) != 64 || a == b {
		t.Errorf("tokens %q %q", a, b)
	}
	addr, err := FreeLoopbackAddr()
	if err != nil || !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("addr = %q, %v", addr, err)
	}
}

func TestStartChildStartFailure(t *testing.T) {
	if _, err := StartChild(filepath.Join(t.TempDir(), "nope"), os.Environ()); err == nil {
		t.Error("starting a missing binary succeeded")
	}
}

func TestChildStopsOnSIGTERM(t *testing.T) {
	env, port := stubEnv(t)
	c, err := StartChild(installStub(t, t.TempDir(), ""), env)
	if err != nil {
		t.Fatal(err)
	}
	waitHTTP(t, "http://127.0.0.1:"+port+"/ready")
	t0 := time.Now()
	if c.Stop(10 * time.Second) {
		t.Error("clean child was killed")
	}
	if d := time.Since(t0); d > 3*time.Second {
		t.Errorf("stop took %v", d)
	}
	select {
	case <-c.Done():
	default:
		t.Error("Done not closed after Stop")
	}
	if c.Err() != nil {
		t.Errorf("exit err = %v, want nil", c.Err())
	}
	if c.Stop(time.Second) {
		t.Error("second Stop reported a kill")
	}
}

func TestChildKilledAfterGrace(t *testing.T) {
	env, port := stubEnv(t)
	c, err := StartChild(installStub(t, t.TempDir(), "ignore_term=1\n"), env)
	if err != nil {
		t.Fatal(err)
	}
	waitHTTP(t, "http://127.0.0.1:"+port+"/ready")
	t0 := time.Now()
	if !c.Stop(500 * time.Millisecond) {
		t.Error("stubborn child was not killed")
	}
	if d := time.Since(t0); d < 400*time.Millisecond || d > 5*time.Second {
		t.Errorf("stop took %v, want ~grace", d)
	}
	if c.Err() == nil {
		t.Error("killed child reported success")
	}
}

func TestChildCrashSeen(t *testing.T) {
	env, _ := stubEnv(t)
	c, err := StartChild(installStub(t, t.TempDir(), "crash_after_ms=100\n"), env)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("crash not observed")
	}
	if c.Err() == nil {
		t.Error("crash had nil error")
	}
}

// R1: the child must survive goroutine and OS thread churn in the parent.
func TestChildSurvivesGoroutineChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("20s")
	}
	t.Parallel()
	env, port := stubEnv(t)
	c, err := StartChild(installStub(t, t.TempDir(), ""), env)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Stop(5 * time.Second)
	waitHTTP(t, "http://127.0.0.1:"+port+"/ready")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); time.Sleep(time.Millisecond); _ = strconv.Itoa(i) }()
		}
		wg.Wait()
		select {
		case <-c.Done():
			t.Fatal("child died during churn")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
