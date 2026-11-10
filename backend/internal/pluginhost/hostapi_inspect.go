package pluginhost

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/url"

	"hatamoto.dev/sdk"
)

// PackageInspector gives the verdict on a downloaded package. It is an
// interface because internal/packages imports this package.
type PackageInspector interface {
	// ArchiveLimit is the largest archive Inspect accepts, in bytes.
	ArchiveLimit() int64
	// Inspect reports what data is and whether signerKey signed it. Trouble is
	// a Problem in the result, never an error.
	Inspect(data []byte, signerKey string) sdk.InspectResult
}

// inspectSlots bounds the archives held in memory at once by packages.inspect,
// across all plugins.
var inspectSlots = make(chan struct{}, 2)

const maxInspectURLLen = 2048

// fnPackagesInspect downloads a package from an https URL on one of the hosts
// http.fetch may call and returns its verdict. The body is never part of the result.
func fnPackagesInspect(ctx context.Context, a *API, e *callEnv, raw jsontext.Value) (any, error) {
	req, err := decodeArgs[sdk.InspectRequest](raw)
	if err != nil {
		return nil, err
	}
	if a.svc.Packages == nil || a.svc.Fetcher == nil {
		return nil, unavailable("packages.inspect")
	}
	if u, perr := url.Parse(req.URL); req.URL == "" || len(req.URL) > maxInspectURLLen || perr != nil || u.Scheme != "https" {
		return nil, herr(sdk.ErrInvalidArgument, "url must be an https URL of at most %d bytes", maxInspectURLLen)
	}
	if !a.limits(e.plugin).fetch.Allow() {
		return nil, herr(sdk.ErrRateLimited, "http.fetch rate limit for this plugin reached; try again shortly")
	}
	select {
	case inspectSlots <- struct{}{}:
		defer func() { <-inspectSlots }()
	default:
		return nil, herr(sdk.ErrRateLimited, "packages.inspect is busy; try again shortly")
	}

	hosts := effectiveHosts(ctx, e.manifest.AllowedHosts)
	if !a.grants.Has(e.plugin, sdk.PermHTTPFetchAny) {
		hosts = withoutWildcard(hosts) // "*" is honoured only with its own grant
	}
	limit := a.svc.Packages.ArchiveLimit()
	var data []byte
	err = a.svc.Fetcher.DownloadPublic(ctx, e.plugin, hosts, req.URL, limit, func(r io.Reader) error {
		var rerr error
		data, rerr = io.ReadAll(r)
		return rerr
	})
	var status *DownloadStatusError
	switch {
	case err == nil:
		return a.svc.Packages.Inspect(data, req.SignerKey), nil
	case errors.Is(err, ErrDownloadTooLarge):
		return inspectProblem(fmt.Sprintf("too large: over %d bytes", limit)), nil
	case errors.As(err, &status):
		return inspectProblem(fmt.Sprintf("download answered %d", status.Status)), nil
	case errors.Is(err, ErrFetchBlocked), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return nil, err
	}
	return inspectProblem("could not download the package"), nil
}

func inspectProblem(msg string) sdk.InspectResult {
	return sdk.InspectResult{Problems: []string{msg}}
}
