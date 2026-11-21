package statichost

import "context"

type hostingKey struct{}

// WithHosting carries a snapshot for one export (Test export in served mode)
// so the render path rewrites islands as in static mode WITHOUT touching the
// process-wide Holder that live visitors use. Origins are normalised like
// Holder.Store.
func WithHosting(ctx context.Context, v StaticHosting) context.Context {
	v.Live, _ = NormalizeOrigin(v.Live)
	v.Site, _ = NormalizeOrigin(v.Site)
	if v.SameHost {
		v.Live = ""
	}
	return context.WithValue(ctx, hostingKey{}, v)
}

// HostingFrom returns the snapshot set by WithHosting, else fallback.
func HostingFrom(ctx context.Context, fallback StaticHosting) StaticHosting {
	if v, ok := ctx.Value(hostingKey{}).(StaticHosting); ok {
		return v
	}
	return fallback
}
