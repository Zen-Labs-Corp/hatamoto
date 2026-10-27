package builder

import "testing"

func TestBootKindsOnlyIn(t *testing.T) {
	without, with := BootKinds(false), BootKinds(true)
	if len(without) != len(with) || len(with) == 0 {
		t.Fatalf("kinds %d vs %d", len(without), len(with))
	}
	any := false
	for i, k := range with {
		if without[i].OnlyIn != "" {
			t.Errorf("%s: onlyIn leaked into the admin payload", k.Kind)
		}
		if k.OnlyIn != "" {
			any = true
		}
	}
	if !any {
		t.Error("no kind reports onlyIn")
	}
}

func TestReadableNodesSkipsInvalid(t *testing.T) {
	got := ReadableNodes([]byte(`{"nodes":[{"id":"a","t":"heading","p":{"text":"x"}},{"id":"b","t":"nope"},null]}`))
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("got %+v", got)
	}
}
