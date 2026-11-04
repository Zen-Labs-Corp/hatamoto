package server

import (
	"errors"
	"fmt"
	"testing"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/plugrun"
)

func TestSuggestionErrMapsDecided(t *testing.T) {
	if err := suggestionErr(fmt.Errorf("x: %w", plugrun.ErrProposalDecided)); !errors.Is(err, admin.ErrReviewSuggestionDecided) {
		t.Errorf("decided: %v", err)
	}
	boom := errors.New("boom")
	if suggestionErr(boom) != boom || suggestionErr(nil) != nil {
		t.Error("other errors pass through")
	}
}

func TestCancelErrMapsEveryRefusal(t *testing.T) {
	for in, want := range map[error]error{
		plugrun.ErrNotFound:             admin.ErrRunNotFound,
		pluginhost.ErrRunsUnavailable:   admin.ErrRunsUnavailable,
		pluginhost.ErrRunNotCancellable: admin.ErrRunNotCancellable,
	} {
		if err := cancelErr(fmt.Errorf("x: %w", in)); !errors.Is(err, want) {
			t.Errorf("%v -> %v, want %v", in, err, want)
		}
	}
	if cancelErr(nil) != nil {
		t.Error("nil stays nil")
	}
}
