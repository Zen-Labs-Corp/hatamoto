package designcheck_test

import (
	"os"
	"strings"
	"testing"

	"hatamoto.dev/core/adminpages/designcheck"
)

func TestCheckExampleModuleIsClean(t *testing.T) {
	t.Parallel()
	if err := designcheck.Check(os.DirFS("../testdata/example")); err != nil {
		t.Fatalf("example module flagged:\n%v", err)
	}
}

func TestCheckFlagsBespokeClass(t *testing.T) {
	t.Parallel()
	err := designcheck.Check(os.DirFS("../testdata/bespoke"))
	if err == nil {
		t.Fatal("bespoke module passed")
	}
	for _, want := range []string{"MOD-01", "bespoke.html"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}
