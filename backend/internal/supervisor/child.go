package supervisor

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Child is one running `hatamoto serve` process.
type Child struct {
	pid  int
	done chan struct{}
	mu   sync.Mutex
	err  error
}

// StartChild runs `<path> serve` with env. Stdout and stderr are inherited.
//
// Pdeathsig fires when the OS thread that forked the child exits, so the
// goroutine that calls Start also waits for the child and stays pinned to its
// thread until the child is dead.
func StartChild(path string, env []string) (*Child, error) {
	cmd := exec.Command(path, "serve")
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	setProc(cmd)

	c := &Child{done: make(chan struct{})}
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := cmd.Start(); err != nil {
			started <- err
			close(c.done)
			return
		}
		c.pid = cmd.Process.Pid
		started <- nil
		err := cmd.Wait()
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
	}()
	if err := <-started; err != nil {
		return nil, err
	}
	return c, nil
}

// Pid is the child's process id.
func (c *Child) Pid() int { return c.pid }

// Done is closed once the child has exited and been reaped.
func (c *Child) Done() <-chan struct{} { return c.done }

// Err is the child's exit status error (nil for exit 0); valid after Done.
func (c *Child) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Stop sends SIGTERM and waits up to grace for the child to exit, then
// SIGKILLs its process group. It reports whether the kill was needed and
// returns only once the child is gone.
func (c *Child) Stop(grace time.Duration) (killed bool) {
	select {
	case <-c.done:
		return false
	default:
	}
	_ = termProc(c.pid)
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-c.done:
		return false
	case <-t.C:
	}
	_ = killGroup(c.pid)
	<-c.done
	return true
}

// ChildEnv is base with HATAMOTO_DATA and the smoke settings replaced by the
// given values; any inherited HATAMOTO_SMOKE_* is dropped. The token travels
// only through the environment, never argv.
func ChildEnv(base []string, data, smokeAddr, token string) []string {
	out := make([]string, 0, len(base)+3)
	for _, kv := range base {
		if strings.HasPrefix(kv, "HATAMOTO_SMOKE_") || strings.HasPrefix(kv, "HATAMOTO_DATA=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"HATAMOTO_DATA="+data,
		"HATAMOTO_SMOKE_ADDR="+smokeAddr,
		"HATAMOTO_SMOKE_TOKEN="+token,
	)
}

// FreeLoopbackAddr returns a 127.0.0.1 address whose port was free a moment ago.
func FreeLoopbackAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer l.Close()
	return l.Addr().String(), nil
}

// NewToken returns a fresh 32-byte hex token.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
