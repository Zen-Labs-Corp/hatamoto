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
