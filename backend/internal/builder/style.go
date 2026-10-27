package builder

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// StyleSpec is the closed style vocabulary of the builder. It is data: the Go
// compiler below turns it into CSS, and StyleSpecTable exposes the same table
// so a client can preview a change without owning a second mapping. Raw CSS is
// never accepted; every value is a token, an enum member or a bounded literal.

type styleKey struct {
	props []string
	kind  string // space | color | enum | count | len | radius | fs | lit | media | grad | gtc
	enum  map[string]string
	// kind count: a whole number in [min,max] written without leading zeros,
	// substituted for %d in tpl.
	tpl      string
	min, max int
	// kind lit: tok names the styleTokens class; a value that is not a token
	// must match pat and is emitted as written.
	pat *regexp.Regexp
	tok string
	// signed (kind space): negative lengths and auto are accepted besides tokens.
	signed bool
	// late emits the key after all other keys of its breakpoint, so a longhand
	// follows the shorthand that would otherwise reset it (cgap after gap, cs
	// after span).
	late bool
	// settle marks a key whose live preview is left to the server: its CSS is
	// not a function of the key alone (display vs hide, grad plus bgimg).
	settle bool
	// states allows the key inside a state (hover, focus, active,
	// parent-hover). Layout, size, spacing and transition keys stay Normal-only
	// so a state never makes the page jump.
	states bool
}

var (
	lenRe = regexp.MustCompile(`^(0|[0-9]{1,4}(\.[0-9]{1,2})?(px|rem|em|%|vh|vw))$`)
	hexRe = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

	// The v2 patterns are portable to JavaScript (no lookaround, no flags): the
	// table serves them to the client unchanged.

	// signedLenRe is a margin or offset: a signed length or auto. lenRe stays
	// unsigned for paddings.
	signedLenRe = regexp.MustCompile(`^(auto|0|-?[0-9]{1,4}(\.[0-9]{1,2})?(px|rem|em|%|vh|vw))$`)
	// scrollLenRe is a scroll margin: a non-negative length without %, which
	// scroll-margin does not take.
	scrollLenRe = regexp.MustCompile(`^(0|[0-9]{1,4}(\.[0-9]{1,2})?(px|rem|em|vh|vw))$`)
	// mediaRe is a root-relative media path of the shape the media service
	// writes. Segments hold no dot, so .. and // cannot match.
	mediaRe = regexp.MustCompile(`^/media/([A-Za-z0-9_-]{1,40}/){0,2}[A-Za-z0-9_-]{1,64}\.(png|jpe?g|webp|avif|gif)$`)
	// gradRe is the closed gradient form: linear-<angle>-<c1>-<c2>[-<c3>] or
	// radial-<c1>-<c2>[-<c3>], a colour being a token or 3/6 hex digits without #.
	gradRe = regexp.MustCompile(`^(linear-([0-9]|[1-9][0-9]|[12][0-9][0-9]|3[0-5][0-9]|360)-` + gradColor + `-` + gradColor + `(-` + gradColor + `)?|radial-` + gradColor + `-` + gradColor + `(-` + gradColor + `)?)$`)
	// gtcRe is a list of up to six grid tracks, each auto or 1fr..12fr.
	gtcRe = regexp.MustCompile(`^(auto|([1-9]|1[0-2])fr)( (auto|([1-9]|1[0-2])fr)){0,5}$`)
	// lhRe is a unitless line height 0.8..3 with up to two decimals.
	lhRe = regexp.MustCompile(`^(0\.[89][0-9]?|[12](\.[0-9]{1,2})?|3(\.0{1,2})?)$`)
	// lsRe is a letter spacing within +-99.99 in px, rem or em.
	lsRe = regexp.MustCompile(`^(0|-?([0-9]|[1-9][0-9])(\.[0-9]{1,2})?(px|rem|em))$`)
	// bwRe is a border width of 0..20px.
	bwRe = regexp.MustCompile(`^(0|([0-9]|1[0-9])(\.[0-9]{1,2})?px|20px)$`)

	// The E2 patterns are bounded like the rest: every literal carries its
	// unit and a range, so a composed transform or filter holds no free text.

	// shiftRe is a translation: 0, up to 400px or up to 100%, signed.
	shiftRe = regexp.MustCompile(`^(0|-?(([0-9]|[1-9][0-9]|[1-3][0-9][0-9])(\.[0-9]{1,2})?|400)px|-?(([0-9]|[1-9][0-9])(\.[0-9]{1,2})?|100)%)$`)
	// scaleRe is a scale factor 0.5..2 with up to two decimals.
	scaleRe = regexp.MustCompile(`^(0\.[5-9][0-9]?|1(\.[0-9]{1,2})?|2(\.0{1,2})?)$`)
	// rotRe is a whole rotation of -360..360deg.
	rotRe = regexp.MustCompile(`^-?(0|[1-9]|[1-9][0-9]|[12][0-9][0-9]|3[0-5][0-9]|360)deg$`)
	// skewRe is a whole skew of -45..45deg.
	skewRe = regexp.MustCompile(`^-?([0-9]|[1-3][0-9]|4[0-5])deg$`)
	// blurRe is a blur of 0..20px.
	blurRe = bwRe
	// bblurRe is a backdrop blur of 0..40px.
	bblurRe = regexp.MustCompile(`^(0|([0-9]|[1-3][0-9])(\.[0-9]{1,2})?px|40px)$`)
	// gainRe is a brightness, contrast or saturation of 0..200%.
	gainRe = regexp.MustCompile(`^(([0-9]|[1-9][0-9]|1[0-9][0-9])(\.[0-9]{1,2})?|200)%$`)
	// grayRe is a grayscale of 0..100%.
	grayRe = regexp.MustCompile(`^(([0-9]|[1-9][0-9])(\.[0-9]{1,2})?|100)%$`)
	// hueRe is a hue rotation of 0..360deg.
	hueRe = regexp.MustCompile(`^(([0-9]|[1-9][0-9]|[12][0-9][0-9]|3[0-5][0-9])(\.[0-9]{1,2})?|360)deg$`)
)

const gradColor = `(primary|bg|text|surface|muted|accent|[0-9a-fA-F]{6}|[0-9a-fA-F]{3})`

// spaceSteps are the built-in spacing scale, indexed by step 0..12.
var spaceSteps = []string{"0", "0.25rem", "0.5rem", "0.75rem", "1rem", "1.25rem", "1.5rem", "2rem", "2.5rem", "3rem", "4rem", "5rem", "6rem"}

func spaceTokens() map[string]string {
	m := make(map[string]string, len(spaceSteps))
	for i, v := range spaceSteps {
		m["space-"+strconv.Itoa(i)] = "var(--hm-space-" + strconv.Itoa(i) + "," + v + ")"
	}
	return m
}

// shadowDefaults are the built-in shadow steps; the tokens of the design
// system override them through --hm-shadow-<step>.
var shadowDefaults = map[string]string{
	"sm": "0 1px 2px rgba(0,0,0,.08)",
	"md": "0 4px 12px rgba(0,0,0,.1)",
	"lg": "0 12px 32px rgba(0,0,0,.14)",
}

func shadowEnum() map[string]string {
	m := map[string]string{"none": "none"}
	for k, v := range shadowDefaults {
		m[k] = "var(--hm-shadow-" + k + "," + v + ")"
	}
	return m
}

var styleKeys = map[string]styleKey{
	"pt":    {props: []string{"padding-top"}, kind: "space"},
	"pb":    {props: []string{"padding-bottom"}, kind: "space"},
	"pl":    {props: []string{"padding-left"}, kind: "space"},
	"pr":    {props: []string{"padding-right"}, kind: "space"},
	"mt":    {props: []string{"margin-top"}, kind: "space", signed: true},
	"mb":    {props: []string{"margin-bottom"}, kind: "space", signed: true},
	"gap":   {props: []string{"gap"}, kind: "space"},
	"bg":    {props: []string{"background-color"}, kind: "color", states: true},
	"color": {props: []string{"color"}, kind: "color", states: true},
	"dir":   {props: []string{"flex-direction"}, kind: "enum", enum: map[string]string{"row": "row", "column": "column", "row-reverse": "row-reverse", "column-reverse": "column-reverse"}},
	"ai":    {props: []string{"align-items"}, kind: "enum", enum: map[string]string{"start": "flex-start", "center": "center", "end": "flex-end", "stretch": "stretch", "baseline": "baseline"}},
	"jc":    {props: []string{"justify-content"}, kind: "enum", enum: map[string]string{"start": "flex-start", "center": "center", "end": "flex-end", "between": "space-between", "around": "space-around", "evenly": "space-evenly"}},
	"wrap":  {props: []string{"flex-wrap"}, kind: "enum", enum: map[string]string{"wrap": "wrap", "nowrap": "nowrap"}},
	"ta":    {props: []string{"text-align"}, kind: "enum", enum: map[string]string{"left": "left", "center": "center", "right": "right", "justify": "justify"}},
	"as":    {props: []string{"align-self"}, kind: "enum", enum: map[string]string{"start": "flex-start", "center": "center", "end": "flex-end", "stretch": "stretch"}},
	// hide is one value, display:none, and cascades desktop-first: a node hidden
	// at md is hidden at sm too. There is no "unhide" value: reverting display
	// would break flex and grid hosts.
	"hide":   {props: []string{"display"}, kind: "enum", enum: map[string]string{"1": "none"}, settle: true},
	"cols":   {props: []string{"grid-template-columns"}, kind: "count", tpl: "repeat(%d,minmax(0,1fr))", min: 1, max: 12, settle: true},
	"rows":   {props: []string{"grid-template-rows"}, kind: "count", tpl: "repeat(%d,auto)", min: 1, max: 12},
	"span":   {props: []string{"grid-column"}, kind: "count", tpl: "span %d", min: 1, max: 12},
	"maxw":   {props: []string{"max-width"}, kind: "len"},
	"w":      {props: []string{"width"}, kind: "len"},
	"minh":   {props: []string{"min-height"}, kind: "len"},
	"radius": {props: []string{"border-radius"}, kind: "radius", states: true},
	"fs":     {props: []string{"font-size"}, kind: "fs"},
	"shadow": {props: []string{"box-shadow"}, kind: "enum", enum: shadowEnum(), states: true},

	// StyleSpec v2.
	// Spacing.
	"ml": {props: []string{"margin-left"}, kind: "space", signed: true},
	"mr": {props: []string{"margin-right"}, kind: "space", signed: true},
	// Size.
	"h":        {props: []string{"height"}, kind: "len"},
	"maxh":     {props: []string{"max-height"}, kind: "len"},
	"minw":     {props: []string{"min-width"}, kind: "len"},
	"aspect":   {props: []string{"aspect-ratio"}, kind: "enum", enum: map[string]string{"auto": "auto", "1/1": "1 / 1", "4/3": "4 / 3", "3/2": "3 / 2", "16/9": "16 / 9", "21/9": "21 / 9", "3/4": "3 / 4", "2/3": "2 / 3", "9/16": "9 / 16"}},
	"fit":      {props: []string{"object-fit"}, kind: "enum", enum: enumSame("cover", "contain", "fill", "none", "scale-down")},
	"objpos":   {props: []string{"object-position"}, kind: "enum", enum: positionEnum()},
	"overflow": {props: []string{"overflow"}, kind: "enum", enum: enumSame("visible", "hidden", "auto", "clip")},
	// Position (fixed is not offered).
	"pos":      {props: []string{"position"}, kind: "enum", enum: enumSame("static", "relative", "absolute", "sticky")},
	"top":      {props: []string{"top"}, kind: "space", signed: true},
	"right":    {props: []string{"right"}, kind: "space", signed: true},
	"bottom":   {props: []string{"bottom"}, kind: "space", signed: true},
	"left":     {props: []string{"left"}, kind: "space", signed: true},
	"z":        {props: []string{"z-index"}, kind: "count", tpl: "%d", min: 0, max: 100},
	"scrollmt": {props: []string{"scroll-margin-top"}, kind: "lit", pat: scrollLenRe},
	// Typography.
	"ff": {props: []string{"font-family"}, kind: "enum", enum: map[string]string{
		"body":    "var(--hm-font,inherit)",
		"heading": "var(--hm-font-heading,var(--hm-font,inherit))",
		"mono":    "var(--hm-font-mono,ui-monospace,SFMono-Regular,Menlo,Consolas,monospace)",
	}},
	"fw":  {props: []string{"font-weight"}, kind: "enum", enum: enumSame("100", "200", "300", "400", "500", "600", "700", "800", "900", "normal", "bold"), states: true},
	"lh":  {props: []string{"line-height"}, kind: "lit", tok: "lh", pat: lhRe},
	"ls":  {props: []string{"letter-spacing"}, kind: "lit", tok: "ls", pat: lsRe},
	"tt":  {props: []string{"text-transform"}, kind: "enum", enum: enumSame("none", "uppercase", "lowercase", "capitalize")},
	"td":  {props: []string{"text-decoration"}, kind: "enum", enum: enumSame("none", "underline", "line-through"), states: true},
	"fst": {props: []string{"font-style"}, kind: "enum", enum: enumSame("normal", "italic")},
	// Border: bw before its sides and radius before its corners in key order.
	"bw":  {props: []string{"border-width"}, kind: "lit", tok: "bw", pat: bwRe, states: true},
	"bwt": {props: []string{"border-top-width"}, kind: "lit", tok: "bw", pat: bwRe, states: true},
	"bwr": {props: []string{"border-right-width"}, kind: "lit", tok: "bw", pat: bwRe, states: true},
	"bwb": {props: []string{"border-bottom-width"}, kind: "lit", tok: "bw", pat: bwRe, states: true},
	"bwl": {props: []string{"border-left-width"}, kind: "lit", tok: "bw", pat: bwRe, states: true},
	"bs":  {props: []string{"border-style"}, kind: "enum", enum: enumSame("none", "solid", "dashed", "dotted", "double"), states: true},
	"bc":  {props: []string{"border-color"}, kind: "color", states: true},
	"rtl": {props: []string{"border-top-left-radius"}, kind: "radius", states: true},
	"rtr": {props: []string{"border-top-right-radius"}, kind: "radius", states: true},
	"rbr": {props: []string{"border-bottom-right-radius"}, kind: "radius", states: true},
	"rbl": {props: []string{"border-bottom-left-radius"}, kind: "radius", states: true},
	// Background. bgimg and grad share one background-image declaration that
	// stylesCSS composes; alone each compiles to its own layer.
	"bgimg":  {props: []string{"background-image"}, kind: "media", pat: mediaRe, settle: true},
	"grad":   {props: []string{"background-image"}, kind: "grad", pat: gradRe, settle: true, states: true},
	"bgpos":  {props: []string{"background-position"}, kind: "enum", enum: positionEnum()},
	"bgsize": {props: []string{"background-size"}, kind: "enum", enum: enumSame("auto", "cover", "contain")},
	"bgrep":  {props: []string{"background-repeat"}, kind: "enum", enum: enumSame("no-repeat", "repeat", "repeat-x", "repeat-y")},
	"bgatt":  {props: []string{"background-attachment"}, kind: "enum", enum: enumSame("scroll", "fixed")},
	// Effects.
	"op": {props: []string{"opacity"}, kind: "enum", enum: opacityEnum(), states: true},
	// Layout. hide wins over display in one breakpoint (key order).
	"display": {props: []string{"display"}, kind: "enum", enum: enumSame("block", "flex", "grid", "inline", "inline-block", "table", "none"), settle: true},
	"order":   {props: []string{"order"}, kind: "count", tpl: "%d", min: -1, max: 12},
	"rgap":    {props: []string{"row-gap"}, kind: "space"},
	"cgap":    {props: []string{"column-gap"}, kind: "space", late: true},
	"gtc":     {props: []string{"grid-template-columns"}, kind: "gtc", pat: gtcRe, settle: true},
	"cs":      {props: []string{"grid-column-start"}, kind: "count", tpl: "%d", min: 1, max: 13, late: true},
	"rs":      {props: []string{"grid-row"}, kind: "count", tpl: "span %d", min: 1, max: 12},
	"js":      {props: []string{"justify-self"}, kind: "enum", enum: enumSame("start", "center", "end", "stretch")},
	"ac":      {props: []string{"align-content"}, kind: "enum", enum: map[string]string{"start": "flex-start", "center": "center", "end": "flex-end", "between": "space-between", "around": "space-around", "evenly": "space-evenly", "stretch": "stretch"}},

	// States and effects (E2). tx..sc compose one transform and blur..hue one
	// filter (see styleGroups); trans..tdelay compose one transition that only
	// stylesCSS emits, inside prefers-reduced-motion: no-preference.
	"tx":       {props: []string{"transform"}, kind: "lit", pat: shiftRe, settle: true, states: true},
	"ty":       {props: []string{"transform"}, kind: "lit", pat: shiftRe, settle: true, states: true},
	"sc":       {props: []string{"transform"}, kind: "lit", pat: scaleRe, settle: true, states: true},
	"rot":      {props: []string{"transform"}, kind: "lit", pat: rotRe, settle: true, states: true},
	"skx":      {props: []string{"transform"}, kind: "lit", pat: skewRe, settle: true, states: true},
	"sky":      {props: []string{"transform"}, kind: "lit", pat: skewRe, settle: true, states: true},
	"torigin":  {props: []string{"transform-origin"}, kind: "enum", enum: positionEnum(), states: true},
	"blur":     {props: []string{"filter"}, kind: "lit", pat: blurRe, settle: true, states: true},
	"bright":   {props: []string{"filter"}, kind: "lit", pat: gainRe, settle: true, states: true},
	"contrast": {props: []string{"filter"}, kind: "lit", pat: gainRe, settle: true, states: true},
	"sat":      {props: []string{"filter"}, kind: "lit", pat: gainRe, settle: true, states: true},
	"gray":     {props: []string{"filter"}, kind: "lit", pat: grayRe, settle: true, states: true},
	"hue":      {props: []string{"filter"}, kind: "lit", pat: hueRe, settle: true, states: true},
	"bblur":    {props: []string{"backdrop-filter", "-webkit-backdrop-filter"}, kind: "lit", pat: bblurRe, settle: true, states: true},
	"blend":    {props: []string{"mix-blend-mode"}, kind: "enum", states: true, enum: enumSame("normal", "multiply", "screen", "darken", "lighten", "color-dodge", "color-burn", "difference", "luminosity")},
	"trans": {props: []string{"transition-property"}, kind: "enum", settle: true, enum: map[string]string{
		"none": "none", "colors": "color,background-color,border-color,text-decoration-color", "opacity": "opacity",
		"shadow": "box-shadow", "transform": "transform", "transform-shadow": "transform,box-shadow", "all": "all",
	}},
	"tdur":   {props: []string{"transition-duration"}, kind: "enum", settle: true, enum: msEnum("75", "100", "150", "200", "300", "500", "700", "1000")},
	"tease":  {props: []string{"transition-timing-function"}, kind: "enum", settle: true, enum: enumSame("linear", "ease", "ease-in", "ease-out", "ease-in-out")},
	"tdelay": {props: []string{"transition-delay"}, kind: "enum", settle: true, enum: msEnum("0", "75", "100", "150", "200", "300", "500")},
}

// msEnum is an enum of milliseconds: the name is the number, the CSS value
// carries the unit.
func msEnum(vs ...string) map[string]string {
	m := make(map[string]string, len(vs))
	for _, v := range vs {
		m[v] = v + "ms"
	}
	return m
}

// enumSame is an enum whose CSS value is its name.
func enumSame(vs ...string) map[string]string {
	m := make(map[string]string, len(vs))
	for _, v := range vs {
		m[v] = v
	}
	return m
}

// positionEnum is the nine positions shared by object-position and
// background-position.
func positionEnum() map[string]string {
	return map[string]string{
		"center": "center", "top": "top", "bottom": "bottom", "left": "left", "right": "right",
		"top-left": "left top", "top-right": "right top", "bottom-left": "left bottom", "bottom-right": "right bottom",
	}
}

// opacityEnum is 0, 0.05 ... 1; the CSS value is the name.
func opacityEnum() map[string]string {
	m := make(map[string]string, 21)
	for i := 0; i <= 20; i++ {
		v := strconv.FormatFloat(float64(i)/20, 'f', -1, 64)
		m[v] = v
	}
	return m
}

var styleTokens = map[string]map[string]string{
	"space": spaceTokens(),
	"color": {
		"primary": "var(--hm-primary)", "bg": "var(--hm-bg)", "text": "var(--hm-text)",
		"surface": "var(--hm-surface,#fff)", "muted": "var(--hm-muted,#eef0f4)", "accent": "var(--hm-accent,#c8102e)",
	},
	"radius": {"sm": "var(--hm-radius-sm)", "md": "var(--hm-radius-md)", "lg": "var(--hm-radius-lg)", "full": "999px"},
	"fs":     {"xs": "0.75rem", "sm": "0.875rem", "md": "1rem", "lg": "1.25rem", "xl": "1.5rem", "2xl": "2rem", "3xl": "2.5rem", "4xl": "3.25rem"},
	"len":    {"container": "var(--hm-container,72rem)", "full": "100%"},
	"lh":     {"tight": "1.1", "snug": "1.25", "normal": "1.5", "relaxed": "1.65", "loose": "2"},
	"ls":     {"normal": "normal"},
	"bw":     {"0": "0", "1": "1px", "2": "2px", "4": "4px"},
}

// breakpointMaxWidth is the max-width in px of each non-base breakpoint.
var breakpointMaxWidth = map[string]int{"md": 1024, "sm": 640}

// breakpointOrder is the emission order: base first, then wider-to-narrower so
// narrower media rules win.
var breakpointOrder = []string{"base", "md", "sm"}

// StyleSpecEntry is one style key of the table served to clients.
type StyleSpecEntry struct {
	Key   string   `json:"key"`
	Kind  string   `json:"kind"`
	Props []string `json:"props"`
	// Tokens maps an allowed token to its CSS value (space, color, radius, fs, len).
	Tokens map[string]string `json:"tokens,omitempty"`
	// Enum maps an allowed value to its CSS value (kind enum).
	Enum map[string]string `json:"enum,omitempty"`
	// Pattern is the regexp of the literal values allowed besides tokens.
	Pattern string `json:"pattern,omitempty"`
	// Min, Max and Tpl describe a numeric key (kind count): a whole number in
	// [Min,Max] replaces %d in Tpl.
	// Min and Max are pointers so a zero bound (z: 0) is still sent.
	Min *int   `json:"min,omitempty"`
	Max *int   `json:"max,omitempty"`
	Tpl string `json:"tpl,omitempty"`
	// Settle marks a key the client does not preview on its own: the server
	// css message settles it.
	Settle bool `json:"settle,omitempty"`
	// States marks a key that may be set inside a state (hover, focus, ...).
	States bool `json:"states,omitempty"`
}

// StyleSpecTable returns the vocabulary as JSON-serialisable data, sorted by key.
// The slice and maps are fresh copies; callers cannot alter the compiler.
func StyleSpecTable() []StyleSpecEntry {
	out := make([]StyleSpecEntry, 0, len(styleKeys))
	for k, sk := range styleKeys {
		e := StyleSpecEntry{Key: k, Kind: sk.kind, Props: append([]string(nil), sk.props...), Settle: sk.settle, States: sk.states}
		switch sk.kind {
		case "enum":
			e.Enum = copyMap(sk.enum)
		case "count":
			e.Min, e.Max, e.Tpl = &sk.min, &sk.max, sk.tpl
		case "color":
			e.Tokens, e.Pattern = copyMap(styleTokens["color"]), hexRe.String()
		case "lit":
			e.Pattern = sk.pat.String()
			if t := styleTokens[sk.tok]; t != nil { // scrollmt takes lengths only
				e.Tokens = copyMap(t)
			}
		case "media", "grad", "gtc":
			e.Pattern = sk.pat.String()
		case "space":
			e.Tokens, e.Pattern = copyMap(styleTokens["space"]), lenRe.String()
			if sk.signed {
				e.Pattern = signedLenRe.String()
			}
		default:
			e.Tokens, e.Pattern = copyMap(styleTokens[sk.kind]), lenRe.String()
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func copyMap(m map[string]string) map[string]string {
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

type decl struct{ prop, val string }

// styleDecl resolves one style key/value to CSS declarations, or an error.
// The returned values come only from the tables above or from a value that
// matched a strict pattern, so they cannot carry CSS punctuation.
func styleDecl(k, v string) ([]decl, error) { return styleDeclWith(k, v, CSSOpts{}) }

func styleDeclWith(k, v string, o CSSOpts) ([]decl, error) {
	sk, ok := styleKeys[k]
	if !ok {
		return nil, fmt.Errorf("unknown style key %q", k)
	}
	var css string
	switch sk.kind {
	case "enum":
		css = sk.enum[v]
	case "count":
		if n, err := strconv.Atoi(v); err == nil && n >= sk.min && n <= sk.max && strconv.Itoa(n) == v {
			css = fmt.Sprintf(sk.tpl, n)
		}
	case "space", "len", "fs", "radius":
		if t := styleTokens[sk.kind][v]; t != "" {
			css = t
			if sk.kind == "fs" && o.Vars {
				css = "var(--hm-fs-" + v + "," + t + ")"
			}
		} else if sk.signed && signedLenRe.MatchString(v) {
			css = v
		} else if !sk.signed && lenRe.MatchString(v) {
			css = v
		}
	case "lit":
		if t := styleTokens[sk.tok][v]; t != "" {
			css = t
		} else if sk.pat.MatchString(v) {
			css = v
		}
	case "media":
		if sk.pat.MatchString(v) && !strings.Contains(v, "..") && !strings.Contains(v, "//") {
			css = `url("` + v + `")`
		}
	case "grad":
		css = gradCSS(sk.pat, v)
	case "gtc":
		if sk.pat.MatchString(v) {
			css = gtcCSS(v)
		}
	case "color":
		if t := styleTokens["color"][v]; t != "" {
			css = t
		} else if hexRe.MatchString(v) {
			css = v
		}
	}
	if css == "" {
		return nil, fmt.Errorf("bad value %q for style %s", v, k)
	}
	out := make([]decl, len(sk.props))
	for i, p := range sk.props {
		out[i] = decl{p, css}
	}
	return out, nil
}

// gradCSS compiles a gradient of the closed grammar, or "" when v is not one.
func gradCSS(re *regexp.Regexp, v string) string {
	if !re.MatchString(v) {
		return ""
	}
	parts := strings.Split(v, "-")
	shape, args := parts[0], parts[1:]
	prefix := "radial-gradient("
	if shape == "linear" {
		prefix = "linear-gradient(" + args[0] + "deg,"
		args = args[1:]
	}
	cols := make([]string, len(args))
	for i, c := range args {
		if t := styleTokens["color"][c]; t != "" {
			cols[i] = t
		} else {
			cols[i] = "#" + strings.ToLower(c)
		}
	}
	return prefix + strings.Join(cols, ",") + ")"
}

// gtcCSS compiles a validated track list: Nfr becomes minmax(0,Nfr), as cols does.
func gtcCSS(v string) string {
	tracks := strings.Split(v, " ")
	for i, t := range tracks {
		if t != "auto" {
			tracks[i] = "minmax(0," + t + ")"
		}
	}
	return strings.Join(tracks, " ")
}

// breakpointSet and stateSet are the closed parts of a style-map key.
var (
	breakpointSet = map[string]bool{"base": true, "md": true, "sm": true}
	stateSet      = map[string]bool{"hover": true, "focus": true, "active": true, "parent-hover": true}
	stateOrder    = []string{"hover", "focus", "active", "parent-hover"}
)

// Breakpoints lists the breakpoints of a style-map key, widest first.
func Breakpoints() []string { return append([]string(nil), breakpointOrder...) }

// States lists the states a style-map key may add after the breakpoint ("md:hover").
func States() []string { return append([]string(nil), stateOrder...) }

// sKeyOrder lists the 15 legal keys of Node.S: per breakpoint the plain key,
// then its states in the order their rules are emitted.
var sKeyOrder = func() []string {
	var ks []string
	for _, bp := range breakpointOrder {
		ks = append(ks, bp)
		for _, st := range stateOrder {
			ks = append(ks, bp+":"+st)
		}
	}
	return ks
}()

// ParseSKey splits a style-map key: "md:hover" gives ("md","hover",true), a
// plain "md" gives ("md","",true). Anything else ("base:", "xl:hover",
// "base:hover:focus", "base:visited") is not a key.
func ParseSKey(k string) (bp, state string, ok bool) {
	bp, state, hasState := strings.Cut(k, ":")
	if !breakpointSet[bp] || (hasState && !stateSet[state]) {
		return "", "", false
	}
	return bp, state, true
}

// sKeys lists the keys of s: the legal ones in sKeyOrder, then any other
// sorted, so a validator can name it.
func sKeys(s map[string]map[string]string) []string {
	var out []string
	for _, k := range sKeyOrder {
		if _, ok := s[k]; ok {
			out = append(out, k)
		}
	}
	var rest []string
	for k := range s {
		if _, _, ok := ParseSKey(k); !ok {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// canParentHover reports whether a node under parent may use parent-hover:
// the parent must exist and be a real element, not a ref or component.
func canParentHover(parent *Node) bool {
	return parent != nil && parent.T != "ref" && parent.T != "component"
}

// checkStyle validates one entry of a style map: the key at, the style key k
// and its value v. parentOK says whether a parent-hover state is allowed.
func checkStyle(at, k, v string, parentOK bool) error {
	_, st, ok := ParseSKey(at)
	if !ok {
		return fmt.Errorf("unknown breakpoint %q", at)
	}
	if _, err := styleDecl(k, v); err != nil {
		return err
	}
	if st != "" && !styleKeys[k].states {
		return fmt.Errorf("style %s is not allowed in the %s state", k, st)
	}
	if st == "parent-hover" && !parentOK {
		return fmt.Errorf("parent-hover needs a parent that is not a ref or component")
	}
	return nil
}

// validateStyles checks every style key and value of a node under parent.
func validateStyles(x *Node, parent *Node) error {
	for _, at := range sKeys(x.S) {
		if _, _, ok := ParseSKey(at); !ok {
			return fmt.Errorf("unknown breakpoint %q", at)
		}
		keys := make([]string, 0, len(x.S[at]))
		for k := range x.S[at] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := checkStyle(at, k, x.S[at][k], canParentHover(parent)); err != nil {
				return err
			}
		}
	}
	return nil
}

// CSSOpts selects the spelling of the compiled CSS.
type CSSOpts struct {
	// Vars compiles the type-scale tokens to var(--hm-fs-<t>,<literal>) so a
	// site design system can override them; off, they compile to the literal.
	Vars bool
	// Force also emits every state rule for a forced selector,
	// [data-hm-force="hover"] and so on, so the canvas can show a state without
	// the pointer. The public render never sets it.
	Force bool
}

var presetRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidPresetName reports whether s is a legal style preset name.
func ValidPresetName(s string) bool { return presetRe.MatchString(s) }

// NodePreset is the style preset a native node references, or "". A component
// ignores presets, and an invalid name is not a reference.
func NodePreset(n *Node) string {
	if n.T == "component" {
		return ""
	}
	if s, ok := n.P["preset"].(string); ok && ValidPresetName(s) {
		return s
	}
	return ""
}

// NodeCSS compiles one node's own styles with the default options.
func NodeCSS(n *Node) string { return NodeCSSWith(n, CSSOpts{}) }

// NodeCSSWith compiles one node's own styles (not its children) into rules for
// the selector .hm-n-<id>. Output order is deterministic. Invalid entries are
// skipped: Validate rejects them on save, and an invalid value is never emitted.
// The id is checked here too, so a doc that skipped Validate cannot inject a selector.
// A node that references a preset doubles the class, so its own values beat the
// preset's at every breakpoint whatever the source order.
func NodeCSSWith(n *Node, o CSSOpts) string { return nodeCSSIn(n, nil, o) }

// nodeCSSIn is NodeCSSWith for a node under parent (nil at top level), whose
// id the parent-hover rules select on. A parent that cannot take parent-hover
// (none, ref, component) leaves those rules out.
func nodeCSSIn(n *Node, parent *Node, o CSSOpts) string {
	if len(n.S) == 0 || !idRe.MatchString(n.ID) {
		return ""
	}
	sel := ".hm-n-" + n.ID
	if NodePreset(n) != "" {
		sel += sel
	}
	parentSel := ""
	if canParentHover(parent) && idRe.MatchString(parent.ID) {
		parentSel = ".hm-n-" + parent.ID
	}
	if n.T == "image" {
		// Whether the node renders wrapped (figure, link, lightbox) depends on bindings that resolve at render,
		// so the picture rules name both shapes: `.hm-n-x img` inside a wrapper, `img.hm-n-x` when bare.
		wrapS, picS, innerS := splitImageStyles(n.S)
		return stylesCSS(sel, parentSel, wrapS, o) +
			stylesCSSForms(sel, sel, parentSel, []selForm{{tail: " img"}, {prefix: "img"}}, picS, o) +
			stylesCSSForms(sel, sel, parentSel, []selForm{{tail: " img"}}, innerS, o)
	}
	return stylesCSS(sel, parentSel, n.S, o)
}

// pictureKeys style the picture itself: on the <img> when the node is wrapped
// (figure, link, lightbox), on the node's own <img> when it is bare.
var pictureKeys = map[string]bool{"aspect": true, "fit": true, "objpos": true, "h": true, "maxh": true, "minh": true}

// roundKeys also reach the <img> inside a wrapper, so a border or background
// of the wrapper rounds with the picture.
var roundKeys = map[string]bool{"radius": true, "rtl": true, "rtr": true, "rbr": true, "rbl": true}

// splitImageStyles divides a style map into the node element's (wrap), the
// picture's under either shape (pic) and the <img> inside a wrapper only
// (inner). The width keys stay on the node element so a link or figure is as
// wide as its picture; inside a wrapper the <img> then fills it.
func splitImageStyles(s map[string]map[string]string) (wrap, pic, inner map[string]map[string]string) {
	wrap, pic, inner = map[string]map[string]string{}, map[string]map[string]string{}, map[string]map[string]string{}
	put := func(dst map[string]map[string]string, at, k, v string) {
		if dst[at] == nil {
			dst[at] = map[string]string{}
		}
		dst[at][k] = v
	}
	trans := map[string]bool{}
	for _, k := range transitionKeys {
		trans[k] = true
	}
	for at, m := range s {
		for k, v := range m {
			switch {
			case pictureKeys[k]:
				put(pic, at, k, v)
				continue
			case trans[k]:
				put(pic, at, k, v)
				put(inner, at, k, v)
			case roundKeys[k]:
				put(inner, at, k, v)
			case k == "w":
				put(inner, at, k, "100%")
			}
			put(wrap, at, k, v)
		}
	}
	return wrap, pic, inner
}

// LightboxSrc reports whether src can be opened by a lightbox link: non-empty
// and a path or http(s) URL (a data: or other scheme is not).
func LightboxSrc(src string) bool {
	src = strings.TrimSpace(src)
	if src == "" {
		return false
	}
	if i := strings.IndexAny(src, ":/?#"); i >= 0 && src[i] == ':' {
		l := strings.ToLower(src[:i])
		return l == "http" || l == "https"
	}
	return !strings.HasPrefix(src, "//") && !strings.HasPrefix(src, `/\`)
}

// composeGroup is a set of keys that compile to one declaration (repeated for
// each of props): the value of
// each present key wrapped in its CSS function, in the fixed order of keys.
type composeGroup struct {
	props []string
	keys  []string
	fns   []string
}

var (
	transformGroup = composeGroup{[]string{"transform"}, []string{"tx", "ty", "rot", "skx", "sky", "sc"},
		[]string{"translateX", "translateY", "rotate", "skewX", "skewY", "scale"}}
	filterGroup = composeGroup{[]string{"filter"}, []string{"blur", "bright", "contrast", "sat", "gray", "hue"},
		[]string{"blur", "brightness", "contrast", "saturate", "grayscale", "hue-rotate"}}
	// backdropGroup is one key, but its value needs blur() and a -webkit- twin.
	backdropGroup = composeGroup{[]string{"backdrop-filter", "-webkit-backdrop-filter"}, []string{"bblur"}, []string{"blur"}}
	// transitionKeys compose the transition rule, which is never part of a body.
	transitionKeys = []string{"trans", "tdur", "tease", "tdelay"}
)

// groupOf maps each composed key to its group.
var groupOf = func() map[string]*composeGroup {
	m := map[string]*composeGroup{}
	for _, g := range []*composeGroup{&transformGroup, &filterGroup, &backdropGroup} {
		for _, k := range g.keys {
			m[k] = g
		}
	}
	return m
}()

func isTransitionKey(k string) bool {
	for _, t := range transitionKeys {
		if k == t {
			return true
		}
	}
	return false
}

// css is the group's declaration value for the cascaded vals, "" when none is set.
func (g *composeGroup) css(vals map[string]string) string {
	var parts []string
	for i, k := range g.keys {
		if v := vals[k]; v != "" {
			parts = append(parts, g.fns[i]+"("+v+")")
		}
	}
	return strings.Join(parts, " ")
}

// cascade resolves keys at breakpoint bp and state: the last valid value of
// each key over the plain breakpoints up to bp, then (for a state) over the
// state's breakpoints up to bp, so a hover rule that adds one transform part
// keeps the others of Normal. own is true when a key is valid at (bp,state)
// itself, or (for a state) when the state has a value from a wider breakpoint
// and Normal sets a key at bp: the wider state rule would otherwise override
// that narrower Normal value. A rule that sets none of them emits nothing.
func cascade(s map[string]map[string]string, bp, state string, keys []string, o CSSOpts) (vals map[string]string, own bool) {
	vals = map[string]string{}
	stateSet, plainHere := false, false
	chains := []string{""}
	if state != "" {
		chains = append(chains, state)
	}
	for _, st := range chains {
		for _, b := range breakpointOrder {
			at := b
			if st != "" {
				at += ":" + st
			}
			for _, k := range keys {
				v, ok := s[at][k]
				if !ok || (st != "" && !styleKeys[k].states) {
					continue
				}
				ds, err := styleDeclWith(k, v, o)
				if err != nil {
					continue
				}
				vals[k] = ds[0].val
				own = own || (b == bp && st == state)
				stateSet = stateSet || st != ""
				plainHere = plainHere || (st == "" && b == bp)
			}
			if b == bp {
				break
			}
		}
	}
	return vals, own || (stateSet && plainHere)
}

// atKey is the key of Node.S for a breakpoint and state.
func atKey(bp, state string) string {
	if state == "" {
		return bp
	}
	return bp + ":" + state
}

// stylesCSS compiles a style map under one selector. parentSel is the
// selector of the parent element for parent-hover rules, "" when there is none.
func stylesCSS(sel, parentSel string, s map[string]map[string]string, o CSSOpts) string {
	return stylesCSSAt(sel, sel, parentSel, s, o)
}

// stylesCSSAt is stylesCSS with a separate selector for the state rules: a
// preset doubles its class there (.hm-p-x.hm-p-x:hover) so its hover beats the
// doubled base rule of a node that references it.
//
// Per breakpoint the rules come in the order plain, transition, hover, focus,
// active, parent-hover; a later breakpoint comes later in the source.
func stylesCSSAt(sel, stateSel, parentSel string, s map[string]map[string]string, o CSSOpts) string {
	return stylesCSSForms(sel, stateSel, parentSel, nil, s, o)
}

// selForm is one shape of a selector: prefix goes before the class (a tag),
// tail after the state (a descendant), so `.a:hover img` and `img.a:hover` can
// share one rule.
type selForm struct{ prefix, tail string }

// stylesCSSForms is stylesCSSAt for rules that name the node in several shapes
// (none: just the selector itself).
func stylesCSSForms(sel, stateSel, parentSel string, forms []selForm, s map[string]map[string]string, o CSSOpts) string {
	if len(forms) == 0 {
		forms = []selForm{{}}
	}
	each := func(base string, f func(prefix, tail string) string) string {
		out := make([]string, len(forms))
		for i, fm := range forms {
			out[i] = f(fm.prefix+base, fm.tail)
		}
		return strings.Join(out, ",")
	}
	var b strings.Builder
	wrap := func(bp, rule string) {
		if bp != "base" {
			rule = "@media (max-width:" + strconv.Itoa(breakpointMaxWidth[bp]) + "px){" + rule + "}"
		}
		b.WriteString(rule)
	}
	for _, bp := range breakpointOrder {
		if body := ruleBody(s, bp, "", o); body != "" {
			wrap(bp, each(sel, func(p, t string) string { return p + t })+"{"+body+"}")
		}
		b.WriteString(transitionRule(each(sel, func(p, t string) string { return p + t }), s, bp, o))
		for _, st := range stateOrder {
			if st == "parent-hover" && parentSel == "" {
				continue
			}
			if body := ruleBody(s, bp, st, o); body != "" {
				wrap(bp, each(stateSel, func(p, t string) string { return stateSelector(p, parentSel, st, o.Force, t) })+"{"+body+"}")
			}
		}
	}
	return b.String()
}

// stateSelector is the selector list of a state rule on sel. With force it
// also lists the forced twin, which has the same specificity as the pseudo-class
// so the cascade is the one of the real state; parent-hover is forced on the
// parent element.
func stateSelector(sel, parentSel, state string, force bool, tail string) string {
	pseudo := map[string]string{"hover": ":hover", "focus": ":focus-visible", "active": ":active"}
	var real, forced string
	if state == "parent-hover" {
		real = parentSel + ":hover " + sel + tail
		forced = parentSel + `[data-hm-force="parent-hover"] ` + sel + tail
	} else {
		real = sel + pseudo[state] + tail
		forced = sel + `[data-hm-force="` + state + `"]` + tail
	}
	if force {
		return real + "," + forced
	}
	return real
}

// ruleBody compiles the declarations of one breakpoint and state: keys in
// alphabetical order (late keys last), the background-image, transform and
// filter groups composed at the slot of their first key, transition keys left
// to transitionRule.
func ruleBody(s map[string]map[string]string, bp, state string, o CSSOpts) string {
	m := s[atKey(bp, state)]
	var body strings.Builder
	done := map[*composeGroup]bool{}
	bgDone := false
	emit := func(g *composeGroup) {
		if vals, own := cascade(s, bp, state, g.keys, o); own {
			if css := g.css(vals); css != "" {
				for _, p := range g.props {
					body.WriteString(p + ":" + css + ";")
				}
			}
		}
	}
	for _, k := range orderedKeys(m) {
		if isTransitionKey(k) || (state != "" && !styleKeys[k].states) {
			continue
		}
		if k == "bgimg" || k == "grad" {
			// One background-image for both keys, at the slot of the first.
			if bgDone {
				continue
			}
			bgDone = true
			if css := backgroundImage(s, bp, state, o); css != "" {
				body.WriteString("background-image:" + css + ";")
			}
			continue
		}
		if g := groupOf[k]; g != nil {
			if done[g] {
				continue
			}
			done[g] = true
			emit(g)
			continue
		}
		ds, err := styleDeclWith(k, m[k], o)
		if err != nil {
			continue
		}
		for _, d := range ds {
			body.WriteString(d.prop + ":" + d.val + ";")
		}
	}
	if state != "" && hasState(s, state) {
		// A state rule of a wider breakpoint is composed again here when Normal
		// narrows a key of the group, so it keeps that narrower value.
		for _, g := range []*composeGroup{&transformGroup, &filterGroup, &backdropGroup} {
			if !done[g] {
				emit(g)
			}
		}
		if !bgDone {
			if css := backgroundImage(s, bp, state, o); css != "" {
				body.WriteString("background-image:" + css + ";")
			}
		}
	}
	return body.String()
}

// hasState reports whether any breakpoint of s has a map for state.
func hasState(s map[string]map[string]string, state string) bool {
	for _, bp := range breakpointOrder {
		if _, ok := s[bp+":"+state]; ok {
			return true
		}
	}
	return false
}

// transitionRule is the transition of the Normal state at bp, "" when bp sets
// none of its keys. It is always wrapped in prefers-reduced-motion:
// no-preference (joined with the breakpoint query, never nested), so a visitor
// who asks for less motion gets none.
func transitionRule(sel string, s map[string]map[string]string, bp string, o CSSOpts) string {
	vals, own := cascade(s, bp, "", transitionKeys, o)
	if !own {
		return ""
	}
	pick := func(k, def string) string {
		if v := vals[k]; v != "" {
			return v
		}
		return def
	}
	body := "transition-property:" + pick("trans", "all") + ";transition-duration:" + pick("tdur", "200ms") +
		";transition-timing-function:" + pick("tease", "ease") + ";transition-delay:" + pick("tdelay", "0ms") + ";"
	q := "(prefers-reduced-motion:no-preference)"
	if bp != "base" {
		q = "(max-width:" + strconv.Itoa(breakpointMaxWidth[bp]) + "px) and " + q
	}
	return "@media " + q + "{" + sel + "{" + body + "}}"
}

// orderedKeys sorts the keys of one breakpoint: alphabetical, then the late
// keys, alphabetical among themselves.
func orderedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	var late []string
	for k := range m {
		if styleKeys[k].late {
			late = append(late, k)
		} else {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(late) > 0 {
		sort.Strings(late)
		keys = append(keys, late...)
	}
	return keys
}

// backgroundImage composes the effective gradient and image at bp and state
// (the nearest valid value, desktop-first) into one background-image value,
// gradient on top. It is "" unless grad or bgimg is set and valid at bp and
// state itself, so a rule that sets neither emits nothing.
func backgroundImage(s map[string]map[string]string, bp, state string, o CSSOpts) string {
	vals, own := cascade(s, bp, state, []string{"grad", "bgimg"}, o)
	if !own {
		return ""
	}
	if vals["grad"] != "" && vals["bgimg"] != "" {
		return vals["grad"] + "," + vals["bgimg"]
	}
	return vals["grad"] + vals["bgimg"]
}

// TreeCSS compiles a subtree into out with the default options.
func TreeCSS(ns []*Node, out map[string]string) { TreeCSSWith(ns, out, CSSOpts{}) }

// TreeCSSWith compiles a subtree into out: node id -> css, only for nodes with styles.
func TreeCSSWith(ns []*Node, out map[string]string, o CSSOpts) { TreeCSSIn(ns, nil, out, o) }

// TreeCSSIn is TreeCSSWith for nodes that sit under parent (nil at top level).
func TreeCSSIn(ns []*Node, parent *Node, out map[string]string, o CSSOpts) {
	for _, n := range ns {
		if n == nil {
			continue
		}
		if c := nodeCSSIn(n, parent, o); c != "" {
			out[n.ID] = c
		}
		TreeCSSIn(n.C, n, out, o)
	}
}
