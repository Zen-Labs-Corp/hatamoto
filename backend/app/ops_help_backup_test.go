package app

import (
	"strings"
	"testing"
)

func TestOpsHelpListsBackupRestore(t *testing.T) {
	for _, want := range []string{"backup restore <id|path.tar>", "--dry-run", "--admin-url DSN", "--config-out P", "backup create [--config P]"} {
		if !strings.Contains(opsUsage, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}
