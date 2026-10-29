package entity

import "context"

type visibleLocalesKey struct{}

// WithVisibleLocales scopes the public reads on ctx to one locale plus shared
// content (LocaleShared). Without it they serve the site language only, so a
// single-locale install never sets it.
func WithVisibleLocales(ctx context.Context, tag string) context.Context {
	return context.WithValue(ctx, visibleLocalesKey{}, []string{tag, LocaleShared})
}

// VisibleLocales returns the locale tags the public reads on ctx may serve, or
// nil for the site language alone. It is the value bound to the placeholder
// given to HiddenPersonFor.
func VisibleLocales(ctx context.Context) []string {
	v, _ := ctx.Value(visibleLocalesKey{}).([]string)
	return v
}
