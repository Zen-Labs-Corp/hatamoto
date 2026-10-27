//go:build unix

package supervisor

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// noFollow makes opens refuse a symlink in the final path element.
const noFollow = syscall.O_NOFOLLOW

// geteuid is a seam for tests.
var geteuid = os.Geteuid

// CheckOwner refuses to run the CLI as a uid other than the owner of
// releases/ (or of the data dir when releases/ does not exist yet). Running
// as someone else (typically root) leaves root-owned state files the service
// user cannot read and opens symlink races inside a directory the service
// user controls.
func CheckOwner(l Layout) error {
	fi, err := os.Lstat(l.Releases)
	if err != nil {
		if fi, err = os.Lstat(l.Data); err != nil {
			return nil // nothing exists yet; whoever runs first becomes the owner
		}
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) == geteuid() {
		return nil
	}
	owner := strconv.Itoa(int(st.Uid))
	if u, err := user.LookupId(owner); err == nil {
		owner = u.Username
	}
	return errors.New(ownerAdvice(fi.Name(), int(st.Uid), owner, geteuid()))
}

// ownerAdvice is the refusal text for a dir owned by ownerUID when the CLI
// runs as caller. It never recommends running as root: that is the mistake
// the check exists to stop. A root-owned dir is one the service user cannot
// write either, so the fix is its ownership, not a different user.
func ownerAdvice(name string, ownerUID int, owner string, caller int) string {
	if ownerUID == 0 {
		return fmt.Sprintf("%s is owned by root, and the service user cannot write to it, so a release switch would never take effect. "+
			"Do not run this command as root; give the volume to the service uid instead "+
			"(e.g. chown -R <service uid> <data dir>; the official image runs as uid 10001), then retry as the service user", name)
	}
	return fmt.Sprintf("%s is owned by %s (uid %d), not by the user running this command (uid %d); "+
		"run it as the owner: sudo -u %s hatamoto update ... (in Docker: docker exec -u %d ...)",
		name, owner, ownerUID, caller, owner, ownerUID)
}
