package sdk

import (
	"strings"
	"testing"
)

func TestManifestCommentsIdentityNeeds(t *testing.T) {
	cases := []struct {
		name    string
		perms   []Permission
		schema  string
		wantErr string
	}{
		{"without comments.read", []Permission{PermEntitiesWriteOwn, PermCommentsIdentity}, "story", string(PermCommentsRead)},
		{"without entity_schema", []Permission{PermCommentsRead, PermCommentsIdentity}, "", "entity_schema"},
		{"complete", []Permission{PermCommentsRead, PermEntitiesWriteOwn, PermCommentsIdentity}, "story", ""},
	}
	for _, c := range cases {
		m := entityManifest(func(m *Manifest) {
			m.Permissions = c.perms
			m.EntitySchema = c.schema
			if c.schema == "" {
				m.EntityFields = nil
			}
		})
		err := m.Validate()
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: refused: %v", c.name, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: err = %v, want one naming %q", c.name, err, c.wantErr)
		}
	}
}
