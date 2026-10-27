package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
)

// errChildExited is RunGate's error when the child died while being gated, as
// opposed to failing a check; the caller counts it as a crash.
var errChildExited = errors.New("child exited during gate")

// errSmokeUnreachable is the smoke call not connecting at all, typically
// because the child could not bind the smoke port.
var errSmokeUnreachable = errors.New("smoke unreachable")

// SmokeResult is the JSON the child's smoke endpoint returns.
type SmokeResult struct {
	Pass         bool         `json:"pass"`
	Checks       []SmokeCheck `json:"checks"`
	RenderErrors int          `json:"render_errors"` // caused by this pass's own requests
	PluginErrors []string     `json:"plugin_errors"`
	// BootFaults are release-level render faults the child logged outside any
	// request: its theme fell back to the default, a rebuild failed, plugin
	// templates were rejected.
	BootFaults []string `json:"boot_faults"`

	// FirstRender is RenderErrors at the first smoke call of a gated child
	// (set by RunGate, not by the child): what one boot plus one smoke pass
	// costs, the figure later runs are compared with.
	FirstRender int `json:"-"`
}

// SmokeCheck is one in-process request the child made against itself.
type SmokeCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Status int    `json:"status"` // HTTP status of the check; 0 when it was skipped
	Detail string `json:"detail"`
}

// GateConfig drives RunGate. Timings are test-shrinkable.
type GateConfig struct {
	ReadyURL  string // child's /ready
	SmokeAddr string // child's loopback smoke listener, host:port
	Token     string

	Started       time.Time // child start; the observation window runs from here (default: now)
	ReadyTimeout  time.Duration
	ReadyPoll     time.Duration // default 500ms
	ObserveWindow time.Duration
	SmokePoll     time.Duration

	// KnownPluginErrors are the plugins that already failed in the last good
	// run; only errors outside this list fail the gate.
	KnownPluginErrors []string

	// Baseline is the per-check status of the last good run (see
	// BaselineOf). Nil means there is no record yet: the gate then only
	// demands no 5xx anywhere and "GET /" at 200 or 404.
	Baseline map[string]int

	// StaleBaseline marks a Baseline older than baselineMaxAge that could not
	// be refreshed (an offline apply has no running child to probe). Editors
	// can unpublish the home page or the sampled page in a day, so a 404 on
	// "GET /" or "published page" is not held against the release. Every other
	// check stays strict, as do boot faults, plugin errors and render errors.
	StaleBaseline bool

	// RenderBaseline is FirstRender of the last good run. Render errors fail
	// the gate only when a smoke call adds more than this.
	RenderBaseline int

	// BootFaultBaseline is the boot fault identifiers of the last good run.
	// Any fault outside it fails the gate: the release broke something at
	// boot that the last one did not.
	BootFaultBaseline []string

	// Initial marks the first smoke of a child that was not switched to: it
	// only decides whether the result is fit to become the baseline, so
	// render errors and plugin errors do not fail it.
	Initial bool
}

// RunGate decides whether a freshly started child is healthy: /ready answers
// 200 within ReadyTimeout, one smoke call passes, and the smoke endpoint keeps
// passing until ObserveWindow has elapsed since the child started. crashed is
// closed when the child exits. The error text is the human reason for failing.
// The last smoke result is returned either way when one was received.
func RunGate(ctx context.Context, g GateConfig, crashed <-chan struct{}) (SmokeResult, error) {
	client := loopbackClient()
	if g.Started.IsZero() {
		g.Started = time.Now()
	}
	if g.ReadyPoll <= 0 {
		g.ReadyPoll = 500 * time.Millisecond
	}
	var last SmokeResult

	sleep := func(d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-crashed:
			return errChildExited
		case <-t.C:
			return nil
		}
	}

	// (a) /ready
	deadline := time.Now().Add(g.ReadyTimeout)
	for {
		status, err := get(ctx, client, g.ReadyURL, "")
		if err == nil && status == http.StatusOK {
			break
		}
		if !time.Now().Before(deadline) {
			why := fmt.Sprintf("HTTP %d", status)
			if err != nil {
				why = err.Error()
			}
			return last, fmt.Errorf("/ready not 200 within %s (%s)", g.ReadyTimeout, why)
		}
		if err := sleep(g.ReadyPoll); err != nil {
			return last, err
		}
	}

	// (b) first smoke call, then (c) observation window
	first := true
	for {
		res, err := smokeOnce(ctx, client, g)
		if err != nil {
			return last, err
		}
		if first {
			res.FirstRender = res.RenderErrors
		} else {
			res.FirstRender = last.FirstRender
		}
		first, last = false, res
		if err := judge(res, g, res.RenderErrors); err != nil {
			return last, err
		}
		remaining := g.ObserveWindow - time.Since(g.Started)
		if remaining <= 0 {
			return last, nil
		}
		if err := sleep(min(g.SmokePoll, remaining)); err != nil {
			return last, err
		}
	}
}

func get(ctx context.Context, c *http.Client, url, token string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, nil
}

func smokeOnce(ctx context.Context, c *http.Client, g GateConfig) (SmokeResult, error) {
	var res SmokeResult
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+g.SmokeAddr+"/smoke", nil)
	if err != nil {
		return res, err
	}
	req.Header.Set("Authorization", "Bearer "+g.Token)
	resp, err := c.Do(req)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errSmokeUnreachable, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("smoke: HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return res, fmt.Errorf("smoke: bad response: %w", err)
	}
	return res, nil
}

// BaselineOf is the record of a good run: check name to HTTP status. Skipped
// checks (status 0) are left out.
func BaselineOf(r SmokeResult) map[string]int {
	m := map[string]int{}
	for _, c := range r.Checks {
		if c.Status > 0 {
			m[c.Name] = c.Status
		}
	}
	return m
}

func goodStatus(code int) bool { return code >= 200 && code < 400 }

// judge turns a smoke result into a gate verdict. Checks are judged against
// the last good run, not against fixed expectations: a box with no home page
// (404 at /) is healthy as long as it stays that way. A 5xx always fails.
func judge(r SmokeResult, g GateConfig, renderAdded int) error {
	var failed []string
	for _, c := range r.Checks {
		switch {
		case c.Status >= 500:
			failed = append(failed, fmt.Sprintf("%s returned %d", c.Name, c.Status))
		case c.Status == 0 && !c.OK:
			failed = append(failed, c.Name+" ("+c.Detail+")")
		}
	}
	if len(failed) == 0 && !r.Pass {
		failed = append(failed, "smoke reported failure")
	}
	if len(failed) == 0 {
		failed = relativeFailures(r, g.Baseline, g.StaleBaseline)
	}
	if len(failed) > 0 {
		return errors.New("smoke failed: " + strings.Join(failed, ", "))
	}
	if g.Initial {
		return nil
	}
	var newFaults []string
	for _, f := range r.BootFaults {
		if !slices.Contains(g.BootFaultBaseline, f) {
			newFaults = append(newFaults, f)
		}
	}
	if len(newFaults) > 0 {
		return fmt.Errorf("smoke: new boot faults (not in the last good run): %s", strings.Join(newFaults, "; "))
	}
	if renderAdded > g.RenderBaseline {
		return fmt.Errorf("smoke: %d render errors in one pass, the last good run had %d", renderAdded, g.RenderBaseline)
	}
	var fresh []string
	for _, p := range r.PluginErrors {
		if !slices.Contains(g.KnownPluginErrors, p) {
			fresh = append(fresh, p)
		}
	}
	if len(fresh) > 0 {
		return errors.New("smoke: new plugin load errors: " + strings.Join(fresh, ", "))
	}
	return nil
}

// baselineMaxAge is how old a stored baseline may be before the gate stops
// forgiving a 404 on the content checks.
const baselineMaxAge = 24 * time.Hour

// contentChecks are the checks an editor can change within a day (unpublish
// the home page or the sampled page); a stale baseline forgives a 404 on them.
var contentChecks = []string{"GET /", "published page"}

// relativeFailures compares r with the baseline. Without one, "GET /" has to
// be 200, 404 or a redirect. With a stale baseline only a 404 on a content
// check is forgiven.
func relativeFailures(r SmokeResult, baseline map[string]int, stale bool) []string {
	now := map[string]SmokeCheck{}
	for _, c := range r.Checks {
		now[c.Name] = c
	}
	var failed []string
	if baseline == nil {
		if c, ok := now["GET /"]; ok && c.Status > 0 && c.Status != 200 && c.Status != 404 && !(c.Status >= 300 && c.Status < 400) {
			failed = append(failed, fmt.Sprintf("GET / returned %d", c.Status))
		}
		return failed
	}
	names := make([]string, 0, len(baseline))
	for n := range baseline {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		was := baseline[n]
		if !goodStatus(was) {
			continue
		}
		c, ok := now[n]
		switch {
		case !ok:
			failed = append(failed, n+" missing (was "+fmt.Sprint(was)+")")
		case c.Status == 0 && c.OK: // skipped
		case stale && c.Status == http.StatusNotFound && slices.Contains(contentChecks, n):
		case !goodStatus(c.Status):
			failed = append(failed, fmt.Sprintf("%s returned %d (was %d)", n, c.Status, was))
		}
	}
	return failed
}

// loopbackClient is the only HTTP client in this package. Its dialer refuses
// any address that is not loopback, checked on the resolved IP right before
// connecting.
func loopbackClient() *http.Client {
	d := &net.Dialer{
		Timeout: 3 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				return fmt.Errorf("supervisor: refusing non-loopback address %s", address)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext:       d.DialContext,
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
