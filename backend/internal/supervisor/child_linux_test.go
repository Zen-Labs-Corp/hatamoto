//go:build linux

package supervisor

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Helper process: starts the stub as its child, prints the child's pid, then
// idles until killed.
func TestPdeathsigHelper(t *testing.T) {
	stub := os.Getenv("SUP_HELPER_STUB")
	if stub == "" {
		t.Skip("helper only")
	}
	c, err := StartChild(stub, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("CHILD " + strconv.Itoa(c.Pid()) + "\n")
	time.Sleep(time.Minute)
}

func procGone(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	// state is the field after the ")" that closes the command name
	s := string(b)
	return strings.HasPrefix(strings.TrimSpace(s[strings.LastIndex(s, ")")+1:]), "Z")
}

func TestChildDiesWithSupervisor(t *testing.T) {
	env, _ := stubEnv(t)
	stub := installStub(t, t.TempDir(), "")
	h := exec.Command(os.Args[0], "-test.run=^TestPdeathsigHelper$")
	h.Env = append(env, "SUP_HELPER_STUB="+stub)
	out, err := h.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	defer h.Process.Kill()
	sc := bufio.NewScanner(out)
	pid := 0
	for sc.Scan() {
		if f, ok := strings.CutPrefix(sc.Text(), "CHILD "); ok {
			pid, _ = strconv.Atoi(f)
			break
		}
	}
	if pid == 0 {
		t.Fatal("helper did not report a child pid")
	}
	if procGone(pid) {
		t.Fatal("child already gone before the kill")
	}
	if err := h.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = h.Wait()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if procGone(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Error("child outlived its killed supervisor (Pdeathsig not effective)")
}
