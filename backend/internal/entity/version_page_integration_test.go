//go:build integration

package entity

import (
	"context"
	"encoding/json/jsontext"
	"testing"
)

// ListVersionsPage returns the newest versions first, counts all of them and
// leaves the attributes out.
func TestListVersionsPageIntegration(t *testing.T) {
	svc, schemaID := condRig(t)
	ctx := context.Background()
	e, err := svc.CreateEntity(ctx, nil, &CreateEntityInput{SchemaID: schemaID, Name: "pg", Attributes: jsontext.Value(`{"n":0}`)})
	if err != nil {
		t.Fatal(err)
	}
	cur := e
	for _, attrs := range []string{`{"n":1}`, `{"n":2}`, `{"n":3}`} {
		if cur, err = condEdit(svc, e.ID, cur.Version, attrs); err != nil {
			t.Fatal(err)
		}
	}

	all, err := svc.ListVersionsPage(ctx, e.ID, OffsetPage{Page: 1, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total < 3 || len(all.Items) != all.Total {
		t.Fatalf("total = %d, items = %d", all.Total, len(all.Items))
	}
	for i, v := range all.Items {
		if v.Attributes != nil {
			t.Fatalf("version %d carries attributes", v.Version)
		}
		if i > 0 && v.Version >= all.Items[i-1].Version {
			t.Fatalf("not newest first: %d after %d", v.Version, all.Items[i-1].Version)
		}
	}

	p2, err := svc.ListVersionsPage(ctx, e.ID, OffsetPage{Page: 2, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if p2.Total != all.Total || len(p2.Items) == 0 || p2.Items[0].Version != all.Items[2].Version {
		t.Fatalf("page 2 = %+v, want to start at version %d", p2, all.Items[2].Version)
	}
}
