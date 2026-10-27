//go:build unix

package supervisor

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// lockRetry is how long a starting supervisor keeps trying for its exclusive
// lock. The CLI's liveness probe holds a shared lock for microseconds; that
// must never make a supervisor exit (in Docker nothing would restart it).
var lockRetry = 3 * time.Second

// pidLock is the exclusive flock a supervisor holds on supervisor.pid for its
// whole life. The lock, not the pid value, says whether a supervisor runs:
// it dies with the process, so a stale or reused pid can never be mistaken
// for a live one, and a supervisor that is PID 1 (Docker) works like any other.
type pidLock struct {
	f    *os.File
	path string
}

// acquirePIDLock takes the lock and writes this process's pid into the file.
func acquirePIDLock(path string) (*pidLock, error) {
	giveUp := time.Now().Add(lockRetry)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|noFollow, 0o644)
		if err != nil {
			return nil, err
		}
		if err := flockNB(f, syscall.LOCK_EX); err != nil {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				if time.Now().Before(giveUp) {
					time.Sleep(20 * time.Millisecond)
					continue
				}
				return nil, fmt.Errorf("another supervisor already holds %s", path)
			}
			return nil, err
		}
		// A previous holder removes the file on a clean exit; a lock on an
		// unlinked inode proves nothing, so start over.
		var a, b syscall.Stat_t
		if syscall.Fstat(int(f.Fd()), &a) != nil || syscall.Stat(path, &b) != nil || a.Ino != b.Ino {
			f.Close()
			continue
		}
		if err := f.Truncate(0); err != nil {
			f.Close()
			return nil, err
		}
		if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
			f.Close()
			return nil, err
		}
		return &pidLock{f: f, path: path}, nil
	}
}

// release removes the file while still holding the lock, then drops it.
func (p *pidLock) release() {
	_ = os.Remove(p.path)
	p.f.Close()
}

func flockNB(f *os.File, how int) error {
	for {
		err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if err != syscall.EINTR {
			return err
		}
	}
}

// pidLockHolder reports the pid in the file when some process holds the lock.
func pidLockHolder(path string) (int, bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|noFollow, 0)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	if err := flockNB(f, syscall.LOCK_SH); err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return 0, false // nobody holds it: not running
	} else if !errors.Is(err, syscall.EWOULDBLOCK) {
		return 0, false
	}
	buf := make([]byte, 32)
	n, _ := f.Read(buf)
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil || pid < 1 {
		return 0, false
	}
	return pid, true
}
