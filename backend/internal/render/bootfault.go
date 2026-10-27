package render

import "sync"

// Boot faults are render problems that belong to the release, not to a
// request: the site's theme falling back to the default theme, a theme that
// cannot be rebuilt, plugin templates rejected, a theme that failed to load.
// They are logged once when the theme is compiled, usually at boot, so no
// request carries them. The smoke endpoint reports the list and the
// supervisor judges it against the last good run.
var bootFaults struct {
	mu   sync.Mutex
	list []string
}

const maxBootFaults = 20

// NoteBootFault records a release-level render fault (deduplicated, capped).
func NoteBootFault(msg string) {
	bootFaults.mu.Lock()
	defer bootFaults.mu.Unlock()
	for _, f := range bootFaults.list {
		if f == msg {
			return
		}
	}
	if len(bootFaults.list) < maxBootFaults {
		bootFaults.list = append(bootFaults.list, msg)
	}
}

// BootFaults returns the faults recorded since the process started.
func BootFaults() []string {
	bootFaults.mu.Lock()
	defer bootFaults.mu.Unlock()
	return append([]string{}, bootFaults.list...)
}
