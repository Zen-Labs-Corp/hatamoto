package pluginhost

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

// A failing render-time filter marks the render degraded; a deliberate
// rejection, a success and a failure of a filter outside rendering do not.
func TestNoteRenderFailure(t *testing.T) {
	ctx := context.Background()
	failing := func(context.Context, any) (any, error) { return nil, errors.New("timeout") }
	rejecting := func(context.Context, any) (any, error) { return nil, &sdk.Reject{Reason: "no"} }
	ok := func(_ context.Context, v any) (any, error) { return v, nil }
	for _, tc := range []struct {
		name string
		hook string
		f    func(context.Context, any) (any, error)
		want bool
	}{
		{"render.head failure", sdk.RenderHead.Name, failing, true},
		{"render.site_graph failure", sdk.RenderSiteGraph.Name, failing, true},
		{"render.site_graph rejection", sdk.RenderSiteGraph.Name, rejecting, false},
		{"jsonld.enrich failure", sdk.JSONLDEnrich.Name, failing, true},
		{"render.page failure", sdk.RenderPage.Name, failing, true},
		{"success", sdk.RenderHead.Name, ok, false},
		{"rejection", sdk.RenderHead.Name, rejecting, false},
		{"not a render-time hook", sdk.EntityBeforeSave.Name, failing, false},
	} {
		before := render.DegradedCount()
		_, _ = noteRenderFailure(tc.hook, tc.f)(ctx, nil)
		if got := render.DegradedCount() != before; got != tc.want {
			t.Errorf("%s: degraded = %v, want %v", tc.name, got, tc.want)
		}
	}
}
