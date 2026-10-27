package release

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// privateField matches a JSON "private" member holding a 64-byte key (88
// base64 characters), the shape packages.Key writes.
var privateField = regexp.MustCompile(`"private"\s*:\s*"[A-Za-z0-9+/]{86}[A-Za-z0-9+/=]{2}"`)

// TestNoTrackedPrivateKeys fails when a tracked file carries private key
// material or a key file name. Nothing is exempt, not .pi/ and not test files:
// tests generate throwaway keys at run time, and plans show key formats with
// placeholders.
func TestNoTrackedPrivateKeys(t *testing.T) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("not in a git checkout")
	}
	root := strings.TrimSpace(string(top))
	list, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skip("git ls-files failed")
	}
	// Built in two pieces so this file does not match its own pattern.
	pemMarker := []byte("-----" + "BEGIN ")
	pemEnd := []byte("PRIVATE KEY" + "-----")
	for _, name := range strings.Split(string(list), "\x00") {
		if name == "" {
			continue
		}
		if strings.HasSuffix(name, ".key") {
			t.Errorf("%s: tracked file looks like a private key file", name)
			continue
		}
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if privateField.Match(b) {
			t.Errorf("%s: contains a JSON private key field", name)
		}
		if i := bytes.Index(b, pemMarker); i >= 0 && bytes.Contains(b[i:], pemEnd) {
			t.Errorf("%s: contains a PEM private key", name)
		}
	}
}
