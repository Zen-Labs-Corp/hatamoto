package reviewstate

import (
	"context"
	"errors"
	"testing"
)

type tooMany struct{}

func (tooMany) Stages(context.Context, Edit) ([]Stage, error) {
	return make([]Stage, maxStages+1), nil
}

func TestStageLimit(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	Register(tooMany{})
	if _, err := StagesFor(context.Background(), Edit{}); !errors.Is(err, ErrPolicy) {
		t.Fatalf("err = %v, want ErrPolicy", err)
	}
}

func TestDefaultStages(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	cases := []struct {
		name string
		e    Edit
		want int
	}{
		{"off", Edit{}, 0},
		{"off bypass", Edit{Bypass: true}, 0},
		{"on", Edit{ReviewEnabled: true}, 1},
		{"on bypass", Edit{ReviewEnabled: true, Bypass: true}, 0},
	}
	for _, c := range cases {
		got, err := StagesFor(context.Background(), c.e)
		if err != nil || got == nil || len(got) != c.want {
			t.Errorf("%s: got %#v, %v; want %d stages", c.name, got, err, c.want)
		}
		if c.want == 1 && got[0] != (Stage{Name: DefaultStageName, Group: DefaultGroup}) {
			t.Errorf("%s: stage = %v", c.name, got[0])
		}
	}
}
