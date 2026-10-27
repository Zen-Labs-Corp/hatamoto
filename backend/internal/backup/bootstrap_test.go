package backup

import (
	"bytes"
	"os"
	"testing"
)

func TestBootstrapSQLMatchesDeploy(t *testing.T) {
	want, err := os.ReadFile("../../../deploy/sql/role-admin-bootstrap.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(bootstrapSQL), want) {
		t.Fatal("embedded copy differs from deploy/sql; re-copy it")
	}
}

func TestWriteTemp0600(t *testing.T) {
	p, err := writeTemp0600(t.TempDir(), "x-*.sql", []byte("select 1"))
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}
