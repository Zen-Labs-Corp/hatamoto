package searchperf

import (
	"context"
	"testing"
)

type fake struct{}

func (fake) Summary(context.Context, Range) (Summary, bool, error) { return Summary{}, false, nil }
func (fake) Pages(context.Context, Range, []string) (map[string]PageStats, error) {
	return nil, nil
}

var _ Provider = fake{}

func TestFakeProviderNoData(t *testing.T) {
	_, ok, err := Provider(fake{}).Summary(context.Background(), Range{})
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
