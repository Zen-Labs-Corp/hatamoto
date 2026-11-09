package designcheck

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Compact density renders the admin at about 90% through one override block in tokens.css.
// These checks keep that block complete (every size token has a compact value) and safe
// (text never below 11px, controls never below the 24px WCAG 2.5.8 target).
// a11y.ParseTokens ignores the compact block on purpose, so this file parses it itself.

var (
	densityRootRx    = regexp.MustCompile(`(?s):root\s*\{(.*?)\n\}`)
	densityCompactRx = regexp.MustCompile(`(?s):root\[data-admin-density="compact"\]\s*\{(.*?)\n\}`)
	densityDeclRx    = regexp.MustCompile(`(--[a-z0-9-]+)\s*:\s*([^;]+);`)
	densityLenRx     = regexp.MustCompile(`^(\d+(?:\.\d+)?)(px|rem)$`)
	densitySizedRx   = regexp.MustCompile(`^--(control-h|badge-h|sidebar-w|subnav-w|sidebar-rail-w|page-|space-|text-|leading-|topbar-h|editor-bar-h|nav-link-h|icon-size)`)
	// Not scaled: hairlines, the alias, and the chip (its remove button must stay a target).
	densityExempt = map[string]bool{"--space-px": true, "--space-0-5": true, "--page-pad": true, "--chip-h": true}
)

const densityTextFloorPx = 11
const densityControlFloorPx = 24

func densityDecls(block string) map[string]string {
	out := map[string]string{}
	for _, m := range densityDeclRx.FindAllStringSubmatch(block, -1) {
		out[m[1]] = strings.TrimSpace(m[2])
	}
	return out
}

// densityPx returns a px or rem length in px; ok is false for unitless, var() and other values.
func densityPx(v string) (float64, bool) {
	m := densityLenRx.FindStringSubmatch(v)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	if m[2] == "rem" {
		n *= 16
	}
	return n, true
}

// checkDensity returns the violations of the compact density rules in a tokens.css text.
func checkDensity(tokensCSS string) []string {
	root := densityRootRx.FindStringSubmatch(tokensCSS)
	if root == nil {
		return []string{"tokens.css has no base :root block"}
	}
	compact := densityCompactRx.FindStringSubmatch(tokensCSS)
	if compact == nil {
		return []string{`tokens.css has no :root[data-admin-density="compact"] block`}
	}
	base, over := densityDecls(root[1]), densityDecls(compact[1])

	var bad []string
	for name, val := range base {
		if !densitySizedRx.MatchString(name) || densityExempt[name] {
			continue
		}
		if _, ok := densityPx(val); !ok {
			continue
		}
		if _, ok := over[name]; !ok {
			bad = append(bad, fmt.Sprintf("%s has no compact value", name))
		}
	}
	for name, val := range over {
		px, ok := densityPx(val)
		if !ok {
			continue
		}
		if strings.HasPrefix(name, "--text-") && px < densityTextFloorPx {
			bad = append(bad, fmt.Sprintf("compact %s is %s: text must stay >= %dpx", name, val, densityTextFloorPx))
		}
		if strings.HasPrefix(name, "--control-h") && px < densityControlFloorPx {
			bad = append(bad, fmt.Sprintf("compact %s is %s: controls must stay >= %dpx", name, val, densityControlFloorPx))
		}
		if bpx, ok := densityPx(base[name]); ok && px > bpx && name != "--text-2xs" {
			bad = append(bad, fmt.Sprintf("compact %s (%s) is larger than comfortable (%s)", name, val, base[name]))
		}
	}
	sort.Strings(bad)
	return bad
}

func TestDensityTokensCompactBlock(t *testing.T) {
	t.Parallel()

	for _, v := range checkDensity(adminAsset(t, "css", "tokens.css")) {
		t.Error(v)
	}
}

func TestDensityCheckFixtures(t *testing.T) {
	t.Parallel()

	const base = ":root {\n  --sidebar-w: 224px;\n  --text-sm: 13px;\n  --control-h: 36px;\n  --space-px: 1px;\n  --radius-md: 8px;\n  --leading-tight: 1.3;\n}\n"
	const compact = ":root[data-admin-density=\"compact\"] {\n  --sidebar-w: 202px; --text-sm: 12px; --control-h: 32px;\n}\n"
	cases := []struct {
		name, css, want string // want empty = clean
	}{
		{"complete", base + compact, ""},
		{"missing sidebar override", base + strings.Replace(compact, "--sidebar-w: 202px; ", "", 1), "--sidebar-w has no compact value"},
		{"text below floor", base + strings.Replace(compact, "--text-sm: 12px", "--text-sm: 10px", 1), "text must stay >= 11px"},
		{"control below target", base + strings.Replace(compact, "--control-h: 32px", "--control-h: 20px", 1), "controls must stay >= 24px"},
		{"compact larger than base", base + strings.Replace(compact, "--text-sm: 12px", "--text-sm: 14px", 1), "larger than comfortable"},
		{"no compact block", base, "no :root[data-admin-density"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := strings.Join(checkDensity(tc.css), "\n")
			if tc.want == "" && got != "" {
				t.Fatalf("unexpected violations: %s", got)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("violations %q lack %q", got, tc.want)
			}
		})
	}
}
