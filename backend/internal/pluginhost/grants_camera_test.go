package pluginhost

import (
	"context"
	"errors"
	"testing"

	"hatamoto.dev/sdk"
)

func TestCameraGrant(t *testing.T) {
	ctx := context.Background()
	gs := NewGrantStore(nil)
	if err := gs.Set(ctx, "p", []sdk.Permission{sdk.PermUICamera}); err != nil {
		t.Fatalf("Set refused the camera grant: %v", err)
	}
	if !gs.Has("p", sdk.PermUICamera) {
		t.Fatal("camera grant not stored")
	}
	if err := gs.Set(ctx, "p", []sdk.Permission{"mic"}); !errors.Is(err, ErrUnknownPermission) {
		t.Fatalf("Set(mic) = %v, want ErrUnknownPermission", err)
	}

	r := newCapaRig(t)
	r.load(t, func(m *sdk.Manifest) { m.UI = &sdk.AdminUI{Title: "Scan"} })
	if _, err := r.rt.Grant(ctx, "capa", []sdk.Permission{sdk.PermUICamera}); !errors.Is(err, ErrNotRequested) {
		t.Fatalf("camera granted without ui.allow: %v", err)
	}
	r2 := newCapaRig(t)
	r2.load(t, func(m *sdk.Manifest) { m.UI = &sdk.AdminUI{Title: "Scan", Allow: []string{sdk.UIAllowCamera}} })
	if _, err := r2.rt.Grant(ctx, "capa", []sdk.Permission{sdk.PermUICamera}); err != nil {
		t.Fatalf("camera grant with ui.allow: %v", err)
	}
	if !r2.rt.Grants().Has("capa", sdk.PermUICamera) {
		t.Fatal("grant not stored")
	}
}
