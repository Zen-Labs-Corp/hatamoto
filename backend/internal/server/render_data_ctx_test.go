package server

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"hatamoto.dev/core/internal/entity"
)

// Template data funcs see shared content next to the default locale on a
// multi-locale site, and an unscoped context on a single-locale one.
func TestRenderDataCtx_VisibleLocales(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name    string
		entries []entity.LocaleEntry
		want    []string
	}{
		{"multi-locale", []entity.LocaleEntry{{Tag: "en"}, {Tag: "fr", Segment: "fr"}}, []string{"en", entity.LocaleShared}},
		{"single locale", []entity.LocaleEntry{{Tag: "en"}}, nil},
		{"no locale config", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := entity.NewService(&countingSchemaLister{}, nil, logger)
			if tt.entries != nil {
				set, err := entity.NewLocaleSet("en", tt.entries, false, "")
				if err != nil {
					t.Fatal(err)
				}
				svc.URLRegistry().SetLocales(func(context.Context) (*entity.LocaleSet, error) { return set, nil })
			}
			d := newRenderData(&Server{entityService: svc, logger: logger})
			ctx, cancel := d.ctx()
			defer cancel()
			if got := entity.VisibleLocales(ctx); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("VisibleLocales = %v, want %v", got, tt.want)
			}
			if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > dataFuncTimeout {
				t.Errorf("deadline = %v, %v; want within %v", dl, ok, dataFuncTimeout)
			}
		})
	}
}
