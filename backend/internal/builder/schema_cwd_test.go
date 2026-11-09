package builder

import (
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
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

func TestCompileKindUnreadableWorkingDir(t *testing.T) {
	chdirUnreadable(t)
	if _, err := compileKind(jsonschema.NewCompiler(), "probe", `{"type":"object","additionalProperties":false,"properties":{"a":{"type":"string"}}}`); err != nil {
		t.Fatalf("compileKind in an unreadable working directory: %v", err)
	}
}
