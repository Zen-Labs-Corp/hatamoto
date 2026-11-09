package pluginhost

import (
	"os"
	"strings"
	"testing"
)

// chdirUnreadable moves the test into a directory the process cannot read, as
// when `sudo -u <service user> hatamoto ...` runs from root's home directory.
func chdirUnreadable(t *testing.T) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Skip("root reads every directory")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := os.Getwd(); err == nil {
		t.Skip("os.Getwd still works here; the case cannot be reproduced")
	}
}

func TestCompileSchemaUnreadableWorkingDir(t *testing.T) {
	chdirUnreadable(t)
	if _, err := CompileSchema([]byte(`{"type":"object","properties":{"token":{"type":"string"}}}`)); err != nil {
		t.Fatalf("CompileSchema in an unreadable working directory: %v", err)
	}
}

func TestCompileSchemaRefToFileRefused(t *testing.T) {
	for _, ref := range []string{"other.json", "file:///etc/hostname"} {
		_, err := CompileSchema([]byte(`{"type":"object","properties":{"token":{"$ref":"` + ref + `"}}}`))
		if err == nil || !strings.Contains(err.Error(), "is not allowed") {
			t.Errorf("$ref %q: err = %v, want an external reference refusal", ref, err)
		}
	}
}
