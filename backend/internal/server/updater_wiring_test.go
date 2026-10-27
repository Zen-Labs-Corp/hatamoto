package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"hatamoto.dev/core/internal/packages"
)

type fakePackages struct {
	cfg    packages.Config
	cfgErr error
	theme  packages.Row
	getErr error
}

func (f fakePackages) List(context.Context) ([]packages.Row, error) { return nil, nil }
func (f fakePackages) Config(context.Context) (packages.Config, error) {
	return f.cfg, f.cfgErr
}
func (f fakePackages) Get(context.Context, packages.Kind, string) (packages.Row, error) {
	return f.theme, f.getErr
}

func TestUpdatePreflightThemeReadFailuresBlock(t *testing.T) {
	ctx := context.Background()
	active := packages.Config{ActiveTheme: "fancy"}
	themeRow := packages.Row{Name: "fancy", Manifest: json.RawMessage(`{"sdk":"^0.10"}`)}

	// Fine: nothing chosen, no config row, or the theme is not installed.
	for name, f := range map[string]fakePackages{
		"no theme":        {},
		"no config row":   {cfgErr: pgx.ErrNoRows},
		"theme not found": {cfg: active, getErr: packages.ErrNotFound},
	} {
		if r, err := (pluginSource{pk: f}).EnabledSDKRanges(ctx); err != nil || len(r) != 0 {
			t.Errorf("%s: %v %v", name, r, err)
		}
	}
	// The theme's range is reported.
	r, err := pluginSource{pk: fakePackages{cfg: active, theme: themeRow}}.EnabledSDKRanges(ctx)
	if err != nil || len(r) != 1 || r[0].Range != "^0.10" {
		t.Fatalf("%v %v", r, err)
	}
	// A failed read must block: it must not let an incompatible theme through.
	for name, f := range map[string]fakePackages{
		"config read fails": {cfgErr: errors.New("connection reset")},
		"theme read fails":  {cfg: active, getErr: errors.New("connection reset")},
	} {
		_, err := pluginSource{pk: f}.EnabledSDKRanges(ctx)
		if err == nil || !strings.Contains(err.Error(), "connection reset") {
			t.Errorf("%s: want an error, got %v", name, err)
		}
	}
}
