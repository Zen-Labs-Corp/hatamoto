package importjob

import (
	"fmt"
	"os"
	"syscall"
)

// checkSpoolDir creates the spool directory and refuses one that somebody
// else prepared: the default lives under the shared system temp dir, where
// another local user could have made the directory first and then read or
// replace the exports spooled in it.
func checkSpoolDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() { // a symlink, say
		return fmt.Errorf("import spool %s is not a plain directory", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("import spool %s belongs to another user", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("import spool %s: %w", dir, err)
		}
	}
	return nil
}
