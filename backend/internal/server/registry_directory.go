package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/permtext"
	"hatamoto.dev/core/internal/render"
)

// The directory is cached for at most directoryTTL so a registry publish shows
// within minutes; a failed fetch is remembered only briefly.
const (
	directoryTTL          = 3 * time.Minute
	directoryFetchTimeout = 5 * time.Second
	directoryFailureTTL   = 15 * time.Second
)

// directoryViews are the plugin and theme directory views of one registry
// fetch.
type directoryViews struct {
	plugins render.PluginDirectoryView
	themes  render.ThemeDirectoryView
}

// registryDirectory feeds plugin_directory and theme_directory sections with
// the plugins and themes of the verified registry. It is separate from the admin Browse registry cache: its
// fetch is bounded by a short timeout, concurrent cold renders share one
// fetch, and an index is never shown after its own expires_at.
type registryDirectory struct {
	registry func() *packages.Registry
	// dev is the optional developer index source; nil when developer packages
	// are not offered. Its failure never affects the main registry's cards.
	dev          registryFetcher
	logger       *slog.Logger
	now          func() time.Time
	fetchTimeout time.Duration

	mu       sync.Mutex
	snap     directorySnapshot
	inflight chan struct{}
	lastLog  string // state last logged: "" before the first fetch, "ok" or an error text
	devLog   string // same for the developer index
}

// registryFetcher reads one verified, already name-filtered index.
type registryFetcher func(ctx context.Context) (*packages.Index, error)

type directorySnapshot struct {
	views directoryViews
	// until is when the snapshot must be refreshed; indexExpires is the signed
	// index's own expiry (zero when there is no usable index).
	until, indexExpires time.Time
}

func newRegistryDirectory(registry func() *packages.Registry, logger *slog.Logger) *registryDirectory {
	return &registryDirectory{registry: registry, logger: logger, now: time.Now, fetchTimeout: directoryFetchTimeout}
}

// pluginDirectory returns the Server's directory provider, built on first use
// so it follows s.packages whenever that is set up.
func (s *Server) pluginDirectory() *registryDirectory {
	s.directoryOnce.Do(func() {
		s.directory = newRegistryDirectory(func() *packages.Registry {
			if s.packages == nil {
				return nil
			}
			return s.packages.Registry
		}, s.logger)
		// The registry config decides, not the install switch: the manager
		// answers ErrRegistryOff when the developer index is off or limited.
		s.directory.dev = func(ctx context.Context) (*packages.Index, error) {
			if s.packages == nil {
				return nil, packages.ErrRegistryOff
			}
			return s.packages.DeveloperIndex(ctx, false)
		}
	})
	return s.directory
}

// View returns the plugin directory state for a render.
func (d *registryDirectory) View(ctx context.Context) render.PluginDirectoryView {
	return d.Views(ctx).plugins
}

// Views returns both directory states for a render from one fetch. It never
// fails: any problem is the unavailable state.
func (d *registryDirectory) Views(ctx context.Context) directoryViews {
	for {
		d.mu.Lock()
		now := d.now()
		if now.Before(d.snap.until) {
			v := d.snap.views
			d.mu.Unlock()
			return v
		}
		if wait := d.inflight; wait != nil {
			d.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return unavailableDirectory()
			}
		}
		d.inflight = make(chan struct{})
		d.mu.Unlock()
		d.refresh(ctx)
	}
}

// cached returns the current snapshot without fetching; ok is false when it is
// missing or stale.
func (d *registryDirectory) cached() (directoryViews, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.now().Before(d.snap.until) {
		return d.snap.views, true
	}
	return directoryViews{}, false
}

func unavailableDirectory() directoryViews {
	return directoryViews{
		plugins: render.PluginDirectoryView{State: render.PluginDirectoryUnavailable},
		themes:  render.ThemeDirectoryView{State: render.PluginDirectoryUnavailable},
	}
}

// refresh fetches once and stores the outcome, then releases the waiters. The
// fetch is detached from the caller's cancellation so one aborted request does
// not fail the others sharing it.
func (d *registryDirectory) refresh(ctx context.Context) {
	idx, registryURL, err := d.fetch(ctx)

	d.mu.Lock()
	defer d.mu.Unlock()
	defer func() {
		close(d.inflight)
		d.inflight = nil
	}()
	now := d.now()
	if errors.Is(err, packages.ErrRegistryOff) {
		// Nothing to fetch and nothing to retry soon: a stable state, not a failure.
		d.snap = directorySnapshot{views: directoryViews{
			plugins: render.PluginDirectoryView{State: render.PluginDirectoryOff},
			themes:  render.ThemeDirectoryView{State: render.PluginDirectoryOff},
		}, until: now.Add(directoryTTL)}
		d.lastLog = "off"
		return
	}
	var exp time.Time
	if err == nil {
		exp, err = time.Parse(time.RFC3339, idx.ExpiresAt)
	}
	if err == nil {
		d.snap = directorySnapshot{views: directoriesFor(idx, registryURL), until: minTime(now.Add(directoryTTL), exp), indexExpires: exp}
		d.logState("ok", nil)
		return
	}
	// Keep showing the last good index until its own expiry, retrying soon.
	if d.snap.indexExpires.After(now) {
		d.snap.until = minTime(now.Add(directoryFailureTTL), d.snap.indexExpires)
	} else {
		d.snap = directorySnapshot{views: unavailableDirectory(), until: now.Add(directoryFailureTTL)}
	}
	d.logState(err.Error(), err)
}

// fetch reads the index with the fetch timeout. A panic in the fetch is turned
// into an error so it is recorded as a failure like any other.
func (d *registryDirectory) fetch(ctx context.Context) (idx *packages.Index, registryURL string, err error) {
	defer func() {
		if p := recover(); p != nil {
			idx, registryURL, err = nil, "", fmt.Errorf("registry fetch panicked: %v", p)
		}
	}()
	reg := d.registry()
	if reg == nil {
		return nil, "", packages.ErrRegistryOff
	}
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.fetchTimeout)
	defer cancel()
	idx, err = reg.Fetch(fctx)
	if err != nil || d.dev == nil {
		return idx, reg.URL, err
	}
	return d.withDeveloper(fctx, idx), reg.URL, nil
}

// withDeveloper adds the developer entries to main. The developer index is
// optional: when it is off, unreadable or expired, main is returned as is.
// Otherwise the result expires with the earlier of the two indexes.
func (d *registryDirectory) withDeveloper(ctx context.Context, main *packages.Index) *packages.Index {
	dev, err := d.devIndex(ctx)
	d.logDeveloper(err)
	if dev == nil {
		return main
	}
	mainExp, err1 := time.Parse(time.RFC3339, main.ExpiresAt)
	devExp, err2 := time.Parse(time.RFC3339, dev.ExpiresAt)
	if err1 != nil || err2 != nil || !devExp.After(d.now()) {
		return main
	}
	merged := *main
	merged.Packages = slices.Clone(main.Packages)
	for _, e := range packages.FilterDeveloper(dev, main).Packages {
		if !developerRevoked(dev, e) {
			merged.Packages = append(merged.Packages, e)
		}
	}
	if devExp.Before(mainExp) {
		merged.ExpiresAt = dev.ExpiresAt
	}
	return &merged
}

// devIndex reads the developer index; a panic is an error, an off index is nil.
func (d *registryDirectory) devIndex(ctx context.Context) (idx *packages.Index, err error) {
	defer func() {
		if p := recover(); p != nil {
			idx, err = nil, fmt.Errorf("developer index fetch panicked: %v", p)
		}
	}()
	idx, err = d.dev(ctx)
	if err != nil {
		return nil, err
	}
	return idx, nil
}

// logDeveloper logs once when the developer index starts failing and once
// when it recovers; an off index is not a failure.
func (d *registryDirectory) logDeveloper(err error) {
	state := "ok"
	if errors.Is(err, packages.ErrRegistryOff) {
		state, err = "off", nil
	} else if err != nil {
		state = err.Error()
	}
	d.mu.Lock()
	prev := d.devLog
	d.devLog = state
	d.mu.Unlock()
	if state == prev || d.logger == nil {
		return
	}
	switch {
	case err != nil:
		d.logger.Warn("plugin directory: developer index unavailable", slog.Any("error", err))
	case state == "ok" && prev != "":
		d.logger.Info("plugin directory: developer index available again")
	}
}

// developerRevoked reports whether dev revokes e by its signer key, or by name
// (and version, when the revocation names one).
func developerRevoked(dev *packages.Index, e packages.Entry) bool {
	var key string
	if pub, err := packages.ParsePublicKey(e.SignerKey); err == nil {
		key = packages.KeyID(pub)
	}
	for _, rv := range dev.Revoked {
		if rv.SignerKey != "" {
			if pub, err := packages.ParsePublicKey(rv.SignerKey); err == nil && key != "" && packages.KeyID(pub) == key {
				return true
			}
		}
		if rv.Name != "" && rv.Name == e.Name && (rv.Version == "" || rv.Version == e.Version) {
			return true
		}
	}
	return false
}

// tierRank orders directory cards: first-party, community, then developer.
func tierRank(tier string) int {
	switch tier {
	case packages.TierFirstParty:
		return 0
	case packages.TierCommunity:
		return 1
	}
	return 2
}

// logState logs when the fetch outcome differs from the last one logged: once
// when the registry starts failing, once when it recovers.
func (d *registryDirectory) logState(state string, err error) {
	if state == d.lastLog {
		return
	}
	prev := d.lastLog
	d.lastLog = state
	if d.logger == nil {
		return
	}
	switch {
	case err != nil:
		d.logger.Warn("plugin directory: registry unavailable", slog.Any("error", err))
	case prev != "":
		d.logger.Info("plugin directory: registry available again")
	}
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// directoriesFor maps one index to both directory views; registryURL anchors
// relative screenshot paths.
func directoriesFor(idx *packages.Index, registryURL string) directoryViews {
	return directoryViews{plugins: directoryView(idx), themes: themeDirectoryView(idx, registryURL)}
}

// themeDirectoryView maps the theme entries of idx to cards, first-party first, then community, then developer, by name within each.
func themeDirectoryView(idx *packages.Index, registryURL string) render.ThemeDirectoryView {
	var cards []render.ThemeCard
	for _, e := range idx.Packages {
		if e.Kind != packages.KindTheme {
			continue
		}
		c := render.ThemeCard{
			Name: e.Name, Title: e.Title, Version: displayVersion(e.Version), Description: e.Description,
			Source: packages.SafeHTTPS(e.Source), Screenshot: packages.ScreenshotURL(registryURL, e.Screenshot),
			Search: strings.ToLower(e.Title + " " + e.Name + " " + e.Description + " " + e.Author),
			About:  aboutParagraphs(e.About), Features: e.Features,
		}
		switch e.Tier {
		case packages.TierFirstParty:
			c.Tier, c.TierLabel = e.Tier, "First-party"
		case packages.TierCommunity:
			c.Tier, c.TierLabel, c.Author = e.Tier, "Community", e.Author
		case packages.TierDeveloper:
			c.Tier, c.TierLabel, c.Author = e.Tier, "Developer", e.Author
		}
		cards = append(cards, c)
	}
	slices.SortStableFunc(cards, func(a, b render.ThemeCard) int {
		return cmp.Or(cmp.Compare(tierRank(a.Tier), tierRank(b.Tier)), cmp.Compare(a.Name, b.Name))
	})
	if len(cards) == 0 {
		return render.ThemeDirectoryView{State: render.PluginDirectoryEmpty}
	}
	return render.ThemeDirectoryView{State: render.PluginDirectoryOK, Cards: cards}
}

// directoryView maps the plugin entries of idx to cards, first-party first, then community, then developer, by name within each.
func directoryView(idx *packages.Index) render.PluginDirectoryView {
	var cards []render.PluginCard
	for _, e := range idx.Packages {
		if e.Kind != packages.KindPlugin {
			continue
		}
		c := render.PluginCard{
			Name: e.Name, Title: e.Title, Version: displayVersion(e.Version), Description: e.Description,
			Source: packages.SafeHTTPS(e.Source),
			Search: strings.ToLower(e.Title + " " + e.Name + " " + e.Description),
			About:  aboutParagraphs(e.About), Features: e.Features,
		}
		pc := permtext.ForCard(e.Permissions)
		c.PermCount, c.PermWarn = pc.Count, pc.Warn
		for _, l := range pc.Lines {
			c.PermLines = append(c.PermLines, render.PermLine(l))
		}
		c.Permissions = e.Permissions
		switch e.Tier {
		case packages.TierFirstParty:
			c.Tier, c.TierLabel = e.Tier, "First-party"
		case packages.TierCommunity:
			c.Tier, c.TierLabel, c.Author = e.Tier, "Community", e.Author
		case packages.TierDeveloper:
			c.Tier, c.TierLabel, c.Author = e.Tier, "Developer", e.Author
			c.Search += " " + strings.ToLower(e.Author)
		}
		cards = append(cards, c)
	}
	slices.SortStableFunc(cards, func(a, b render.PluginCard) int {
		return cmp.Or(cmp.Compare(tierRank(a.Tier), tierRank(b.Tier)), cmp.Compare(a.Name, b.Name))
	})
	if len(cards) == 0 {
		return render.PluginDirectoryView{State: render.PluginDirectoryEmpty}
	}
	return render.PluginDirectoryView{State: render.PluginDirectoryOK, Cards: cards}
}

// aboutParagraphs splits the index's about text (paragraphs joined by a single
// newline) for the detail page; no text, no paragraphs.
func aboutParagraphs(about string) []string {
	if about == "" {
		return nil
	}
	return strings.Split(about, "\n")
}

// displayVersion shows a version as "v1.2.0" whether or not the index has the "v".
func displayVersion(v string) string {
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}
