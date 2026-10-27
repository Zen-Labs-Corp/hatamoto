package builder

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var updateV2Golden = flag.Bool("update-v2-golden", false, "rewrite testdata/style_v2/*.golden")

// v2Case lists the accepted values of one StyleSpec v2 key (the first three are
// used at base, md and sm in the goldens) and values that must be refused.
type v2Case struct {
	key  string
	good []string
	bad  []string
}

// v2Groups are the new keys by inspector group; each group has one golden file.
var v2Groups = []struct {
	name  string
	cases []v2Case
}{
	{"spacing", []v2Case{
		{"ml", []string{"auto", "-1rem", "space-4", "0", "12px"}, []string{"-", "1", "--1px", "-1", "- 1px", "auto ", "AUTO", "-1e3px"}},
		{"mr", []string{"auto", "-2%", "space-0"}, []string{"-1", "10", "auto;"}},
		{"mt", []string{"auto", "-10px", "space-2"}, []string{"1", "-5"}},
		{"mb", []string{"-0.5rem", "auto", "space-12"}, []string{"-1", "5"}},
	}},
	{"size", []v2Case{
		{"h", []string{"50vh", "container", "full", "0"}, []string{"-1px", "auto", "100"}},
		{"maxh", []string{"40rem", "full", "90%"}, []string{"-1rem", "none"}},
		{"minw", []string{"10rem", "container", "0"}, []string{"-1rem", "fit-content"}},
		{"aspect", []string{"16/9", "1/1", "auto", "9/16", "4/3", "3/2", "21/9", "3/4", "2/3"}, []string{"16 / 9", "0/1", "1/0", "2/1", "1:1"}},
		{"fit", []string{"cover", "contain", "fill", "none", "scale-down"}, []string{"inherit", "COVER"}},
		{"objpos", []string{"center", "top-left", "bottom-right", "top", "bottom", "left", "right", "top-right", "bottom-left"}, []string{"50% 50%", "left top", "middle"}},
		{"overflow", []string{"hidden", "auto", "clip", "visible"}, []string{"scroll", "hidden auto"}},
	}},
	{"position", []v2Case{
		{"pos", []string{"sticky", "relative", "absolute", "static"}, []string{"fixed", "inherit", "Sticky"}},
		{"top", []string{"0", "-1rem", "space-4", "auto"}, []string{"1", "-1", "5px;"}},
		{"right", []string{"0", "1rem", "auto"}, []string{"-1", "10"}},
		{"bottom", []string{"-10px", "space-1", "0"}, []string{"calc(1px)", "-"}},
		{"left", []string{"50%", "auto", "-5%"}, []string{"50", "--5%"}},
		{"z", []string{"0", "10", "100"}, []string{"101", "-1", "01", "-0", "1.5", "+1"}},
		{"scrollmt", []string{"5rem", "0", "80px", "10vh"}, []string{"-5rem", "auto", "50%", "full", "container"}},
	}},
	{"typography", []v2Case{
		{"ff", []string{"body", "heading", "mono"}, []string{"Arial", "serif", "var(--x)", "'Arial'"}},
		{"fw", []string{"700", "400", "bold", "normal", "100", "900", "200", "300", "500", "600", "800"}, []string{"1000", "50", "bolder", "750"}},
		{"lh", []string{"normal", "1.4", "tight", "snug", "relaxed", "loose", "0.8", "3", "2.25", "1"}, []string{"0.7", "3.1", "4", "1.123", "01.5", "1.", ".9", "1.5em", "1.5px", "-1"}},
		{"ls", []string{"normal", "0.05em", "-1px", "2px", "0", "99.99px"}, []string{"100px", "-100px", "1", "1%", "0.05"}},
		{"tt", []string{"uppercase", "none", "lowercase", "capitalize"}, []string{"full-width", "UPPERCASE"}},
		{"td", []string{"underline", "none", "line-through"}, []string{"overline", "underline red"}},
		{"fst", []string{"italic", "normal"}, []string{"oblique", "oblique 10deg"}},
	}},
	{"border", []v2Case{
		{"bw", []string{"1", "0", "2", "4", "3px", "20px"}, []string{"3", "21px", "-1px", "1rem", "thin", "20.5px"}},
		{"bwt", []string{"2", "1px", "0"}, []string{"5", "21px"}},
		{"bwr", []string{"4", "10px", "1"}, []string{"-1px", "medium"}},
		{"bwb", []string{"1", "0", "20px"}, []string{"30px"}},
		{"bwl", []string{"2", "7.5px", "4"}, []string{"7.555px", "3"}},
		{"bs", []string{"solid", "dashed", "dotted", "double", "none"}, []string{"groove", "solid red", "hidden"}},
		{"bc", []string{"accent", "#c8102e", "muted", "primary"}, []string{"red", "#ggg", "currentColor", "#12"}},
		{"rtl", []string{"md", "4px", "full", "sm", "lg"}, []string{"-1px", "50", "1px 2px"}},
		{"rtr", []string{"sm", "0", "lg"}, []string{"10", "1px 2px"}},
		{"rbr", []string{"lg", "8px", "md"}, []string{"auto"}},
		{"rbl", []string{"full", "0", "sm"}, []string{"-4px"}},
	}},
	{"background", []v2Case{
		{"bgimg", []string{"/media/a.jpg", "/media/2e0b8f2a-0903-4817-a21f-ed8a69a1e6f3/0123456789abcdef0123456789abcdef.webp", "/media/d/h_800.png", "/media/x/y/z.avif", "/media/a.jpeg", "/media/a.gif"}, []string{
			"/media/a.svg", "/media/a.JPG", "/media/a.jpg.php", "/media/../a.jpg", "/media/a/../b.jpg", "/media//a.jpg", "//evil.example/a.jpg",
			"http://x/media/a.jpg", "https://x/media/a.jpg", "data:image/png;base64,AAAA", "/media/a.jpg)", `/media/a".jpg`, "/media/a'.jpg", "/media/a b.jpg",
			"/media/a.jpg?x=1", "/media/a.jpg#x", "/uploads/a.jpg", "media/a.jpg", "/media/", "/media/.jpg", "/media/a/b/c/d.jpg", "/media/a.jpg\n", `/media/a\.jpg`, "url(/media/a.jpg)",
			"/media/" + strings.Repeat("a", 65) + ".jpg", "/media/" + strings.Repeat("a", 41) + "/b.jpg"}},
		{"grad", []string{"linear-180-000-accent", "radial-fff-primary", "linear-0-ABCDEF-surface-muted", "radial-text-bg-accent", "linear-360-123-456", "linear-45-primary-bg"}, []string{
			"linear-361-000-fff", "linear-0180-000-fff", "linear-00-000-fff", "linear--1-000-fff", "linear-180-000", "linear-180-000-fff-000-fff", "radial-000", "radial-000-fff-000-fff",
			"conic-000-fff", "linear-180-#000-fff", "linear-180-red-blue", "linear-180-00-fff", "linear-180-0000-fff", "linear-180-000-fff;", "linear-180-000-fff)", "linear-180-url-fff", "linear-180deg-000-fff", " linear-180-000-fff", "radial--000-fff"}},
		{"bgpos", []string{"center", "top-left", "bottom", "left", "right", "top", "top-right", "bottom-left", "bottom-right"}, []string{"50% 50%", "center center", "left top"}},
		{"bgsize", []string{"cover", "contain", "auto"}, []string{"100%", "50% 50%"}},
		{"bgrep", []string{"no-repeat", "repeat", "repeat-x", "repeat-y"}, []string{"space", "round", "repeat no-repeat"}},
		{"bgatt", []string{"scroll", "fixed"}, []string{"local", "fixed;"}},
	}},
	{"effects", []v2Case{
		{"op", []string{"0", "0.5", "1", "0.05", "0.95", "0.25"}, []string{"1.05", "0.0", "00", ".5", "50%", "-0.5", "0.52", "2", "1.0"}},
	}},
	{"transform", []v2Case{
		{"tx", []string{"10px", "-50%", "0", "400px", "-400px", "100%", "12.5px"}, []string{"401px", "-401px", "101%", "10", "auto", "1e3px", "10rem", "-", "10 px", "--1px", ".5px", "1.123px", "10px;"}},
		{"ty", []string{"-4px", "5%", "0", "-100%", "399.99px"}, []string{"400.5px", "-101%", "4", "-4 px", "calc(1px)"}},
		{"sc", []string{"1.02", "0.5", "2", "1", "1.5", "0.99", "2.00"}, []string{"0.49", "2.01", "3", "0", "-1", "1.123", ".5", "1.", "02", "1x"}},
		{"rot", []string{"-15deg", "90deg", "360deg", "0deg", "-360deg", "45deg"}, []string{"361deg", "-361deg", "90", "1.5deg", "90rad", "90 deg", "05deg", "--1deg", "400deg"}},
		{"skx", []string{"10deg", "-45deg", "0deg", "45deg"}, []string{"46deg", "-46deg", "10", "1.5deg", "090deg"}},
		{"sky", []string{"-5deg", "45deg", "0deg"}, []string{"50deg", "5", "5grad"}},
		{"torigin", []string{"center", "top-left", "bottom-right", "top", "bottom", "left", "right", "top-right", "bottom-left"}, []string{"50% 50%", "left top", "middle", "center center"}},
	}},
	{"filter", []v2Case{
		{"blur", []string{"4px", "0", "20px", "7.5px"}, []string{"21px", "-1px", "4", "4rem", "20.5px"}},
		{"bright", []string{"120%", "0%", "200%", "99.5%"}, []string{"201%", "-1%", "120", "1.2", "0120%"}},
		{"contrast", []string{"150%", "100%", "0%"}, []string{"250%", "100", "-5%"}},
		{"sat", []string{"80%", "200%", "0%"}, []string{"300%", "80", "8e1%"}},
		{"gray", []string{"100%", "0%", "50%"}, []string{"101%", "50", "-1%", "150%"}},
		{"hue", []string{"90deg", "0deg", "360deg", "12.5deg"}, []string{"361deg", "-10deg", "90", "90rad", "400deg"}},
		{"bblur", []string{"8px", "40px", "0", "12.5px"}, []string{"41px", "-1px", "8", "8rem", "40.5px"}},
		{"blend", []string{"multiply", "normal", "screen", "darken", "lighten", "color-dodge", "color-burn", "difference", "luminosity"}, []string{"hue", "soft-light", "plus-lighter", "Multiply", "multiply;", "multiply, screen"}},
	}},
	{"transition", []v2Case{
		{"trans", []string{"transform-shadow", "none", "colors", "opacity", "shadow", "transform", "all"}, []string{"width", "all 1s", "transform,box-shadow", "ALL", "color", "transform shadow"}},
		{"tdur", []string{"200", "75", "100", "150", "300", "500", "700", "1000"}, []string{"0", "250", "200ms", "2s", "1001", "-1", "0200"}},
		{"tease", []string{"ease-out", "linear", "ease", "ease-in", "ease-in-out"}, []string{"cubic-bezier(0,0,1,1)", "steps(4)", "Ease", "ease out", "ease;"}},
		{"tdelay", []string{"0", "75", "100", "150", "200", "300", "500"}, []string{"1000", "-75", "75ms", "1s", "00"}},
	}},
	{"layout", []v2Case{
		{"display", []string{"flex", "block", "none", "grid", "inline", "inline-block", "table"}, []string{"contents", "inherit", "flex;", "none !important", "Flex"}},
		{"order", []string{"-1", "0", "12"}, []string{"13", "-2", "-0", "01", "1.5", "+1"}},
		{"rgap", []string{"space-2", "1rem", "0"}, []string{"-1rem", "auto"}},
		{"cgap", []string{"space-3", "2rem", "0"}, []string{"-1rem", "auto"}},
		{"gtc", []string{"1fr 2fr", "auto 1fr", "1fr", "auto", "12fr", "1fr 1fr 1fr 1fr 1fr 1fr"}, []string{"1fr  2fr", " 1fr", "1fr ", "0fr", "13fr", "1fr 1fr 1fr 1fr 1fr 1fr 1fr", "1.5fr", "100px", "minmax(0,1fr)", "repeat(3,1fr)", "1fr,2fr", "1fr\n2fr", "auto auto auto auto auto auto auto"}},
		{"cs", []string{"1", "13", "5"}, []string{"0", "14", "-1", "01", "x"}},
		{"rs", []string{"1", "12", "3"}, []string{"0", "13", "-1", "02"}},
		{"js", []string{"start", "center", "end", "stretch"}, []string{"baseline", "left", "between"}},
		{"ac", []string{"start", "center", "end", "between", "around", "evenly", "stretch"}, []string{"baseline", "left", "space-between"}},
	}},
}

func v2Node(c v2Case, preset bool) *Node {
	n := &Node{ID: "g", T: "container", S: map[string]map[string]string{}}
	for i, bp := range breakpointOrder {
		n.S[bp] = map[string]string{c.key: c.good[i%len(c.good)]}
	}
	if preset {
		n.P = map[string]any{"preset": "card"}
	}
	return n
}

func TestV2KeysCoverTheTable(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range v2Groups {
		for _, c := range g.cases {
			seen[c.key] = true
		}
	}
	for k := range oldKeyValues {
		delete(seen, k) // mt and mb are old keys that gained auto and negatives
	}
	for _, k := range []string{"mt", "mb"} {
		seen[k] = true
	}
	for k := range styleKeys {
		if _, old := oldKeyValues[k]; old && k != "mt" && k != "mb" {
			continue
		}
		if !seen[k] {
			t.Errorf("v2 key %s has no case", k)
		}
	}
	if n := len(styleKeys) - len(oldKeyValues); n != 69 {
		t.Errorf("%d new keys, want 69", n)
	}
}

func TestV2ValidateAcceptsAndRefuses(t *testing.T) {
	for _, g := range v2Groups {
		for _, c := range g.cases {
			for _, v := range c.good {
				if _, err := styleDecl(c.key, v); err != nil {
					t.Errorf("%s=%q refused: %v", c.key, v, err)
				}
				if err := styled("md", c.key, v).Validate(); err != nil {
					t.Errorf("Validate %s=%q: %v", c.key, v, err)
				}
			}
			for _, v := range c.bad {
				if _, err := styleDecl(c.key, v); err == nil {
					t.Errorf("%s=%q accepted", c.key, v)
				}
				if err := styled("sm", c.key, v).Validate(); !errors.Is(err, ErrInvalid) {
					t.Errorf("Validate accepted %s=%q: %v", c.key, v, err)
				}
				if css := NodeCSS(styled("base", c.key, v).Nodes[0]); css != "" {
					t.Errorf("render emitted %s=%q: %s", c.key, v, css)
				}
			}
			for _, v := range injections {
				if _, err := styleDecl(c.key, v); err == nil {
					t.Errorf("%s accepted injection %q", c.key, v)
				}
			}
		}
	}
}

// The v2 goldens: every key at base, md and sm, plain and on a preset node.
func TestV2KeyGoldens(t *testing.T) {
	for _, g := range v2Groups {
		var b strings.Builder
		for _, c := range g.cases {
			fmt.Fprintf(&b, "%s\tbase=%s md=%s sm=%s\n", c.key, c.good[0], c.good[1%len(c.good)], c.good[2%len(c.good)])
			fmt.Fprintf(&b, "%s\t%s\n", c.key, NodeCSS(v2Node(c, false)))
			fmt.Fprintf(&b, "%s+preset\t%s\n", c.key, NodeCSS(v2Node(c, true)))
		}
		path := "testdata/style_v2/" + g.name + ".golden"
		if *updateV2Golden {
			if err := os.MkdirAll("testdata/style_v2", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if b.String() != string(want) {
			t.Errorf("%s golden differs (rerun with -update-v2-golden and review):\ngot\n%s\nwant\n%s", g.name, b.String(), want)
		}
	}
}

func nodeWith(s map[string]map[string]string, preset bool) *Node {
	n := &Node{ID: "g", T: "container", S: s}
	if preset {
		n.P = map[string]any{"preset": "card"}
	}
	return n
}

type bp = map[string]string

func TestV2CompositionGoldens(t *testing.T) {
	const (
		accent = "var(--hm-accent,#c8102e)"
		img    = `url("/media/a.jpg")`
		gradA  = "linear-gradient(180deg,#000," + accent + ")"
		radial = "radial-gradient(#fff,var(--hm-primary))"
	)
	cases := []struct {
		name string
		s    map[string]map[string]string
		want string
	}{
		{"sticky header", map[string]map[string]string{"base": {"pos": "sticky", "top": "0", "z": "10", "bg": "surface"}},
			".hm-n-g{background-color:var(--hm-surface,#fff);position:sticky;top:0;z-index:10;}"},
		{"mobile only", map[string]map[string]string{"base": {"hide": "1"}, "sm": {"display": "flex"}},
			".hm-n-g{display:none;}@media (max-width:640px){.hm-n-g{display:flex;}}"},
		{"desktop only", map[string]map[string]string{"md": {"hide": "1"}},
			"@media (max-width:1024px){.hm-n-g{display:none;}}"},
		{"tablet only", map[string]map[string]string{"base": {"hide": "1"}, "md": {"display": "block"}, "sm": {"hide": "1"}},
			".hm-n-g{display:none;}@media (max-width:1024px){.hm-n-g{display:block;}}@media (max-width:640px){.hm-n-g{display:none;}}"},
		{"hide beats display in one breakpoint", map[string]map[string]string{"sm": {"display": "flex", "hide": "1"}},
			"@media (max-width:640px){.hm-n-g{display:flex;display:none;}}"},
		{"narrower display beats wider hide", map[string]map[string]string{"md": {"hide": "1"}, "sm": {"display": "grid"}},
			"@media (max-width:1024px){.hm-n-g{display:none;}}@media (max-width:640px){.hm-n-g{display:grid;}}"},
		{"gtc beats cols", map[string]map[string]string{"base": {"gtc": "2fr 1fr", "cols": "3"}},
			".hm-n-g{grid-template-columns:repeat(3,minmax(0,1fr));grid-template-columns:minmax(0,2fr) minmax(0,1fr);}"},
		{"cgap after gap", map[string]map[string]string{"base": {"cgap": "space-4", "gap": "space-1", "rgap": "space-2"}},
			".hm-n-g{gap:var(--hm-space-1,0.25rem);row-gap:var(--hm-space-2,0.5rem);column-gap:var(--hm-space-4,1rem);}"},
		{"cs after span", map[string]map[string]string{"base": {"cs": "2", "span": "3", "rs": "2"}},
			".hm-n-g{grid-row:span 2;grid-column:span 3;grid-column-start:2;}"},
		{"corners after radius", map[string]map[string]string{"base": {"rtl": "0", "radius": "md", "rbr": "sm"}},
			".hm-n-g{border-radius:var(--hm-radius-md);border-bottom-right-radius:var(--hm-radius-sm);border-top-left-radius:0;}"},
		{"sides after bw", map[string]map[string]string{"base": {"bwt": "4", "bw": "1", "bs": "solid", "bc": "accent"}},
			".hm-n-g{border-color:" + accent + ";border-style:solid;border-width:1px;border-top-width:4px;}"},
		{"bgimg alone", map[string]map[string]string{"base": {"bgimg": "/media/a.jpg"}},
			".hm-n-g{background-image:" + img + ";}"},
		{"grad alone", map[string]map[string]string{"base": {"grad": "linear-180-000-accent"}},
			".hm-n-g{background-image:" + gradA + ";}"},
		{"grad over bgimg", map[string]map[string]string{"base": {"grad": "linear-180-000-accent", "bgimg": "/media/a.jpg", "bgsize": "cover", "bg": "muted"}},
			".hm-n-g{background-color:var(--hm-muted,#eef0f4);background-image:" + gradA + "," + img + ";background-size:cover;}"},
		{"sm-only grad keeps base bgimg", map[string]map[string]string{"base": {"bgimg": "/media/a.jpg"}, "sm": {"grad": "radial-FFF-primary"}},
			".hm-n-g{background-image:" + img + ";}@media (max-width:640px){.hm-n-g{background-image:" + radial + "," + img + ";}}"},
		{"sm-only bgimg keeps base grad", map[string]map[string]string{"base": {"grad": "linear-180-000-accent"}, "sm": {"bgimg": "/media/a.jpg"}},
			".hm-n-g{background-image:" + gradA + ";}@media (max-width:640px){.hm-n-g{background-image:" + gradA + "," + img + ";}}"},
		{"md grad over base grad and img", map[string]map[string]string{"base": {"grad": "linear-180-000-accent", "bgimg": "/media/a.jpg"}, "md": {"grad": "radial-fff-primary"}},
			".hm-n-g{background-image:" + gradA + "," + img + ";}@media (max-width:1024px){.hm-n-g{background-image:" + radial + "," + img + ";}}"},
		{"breakpoint without either key emits no image", map[string]map[string]string{"base": {"bgimg": "/media/a.jpg"}, "md": {"bgsize": "contain"}},
			".hm-n-g{background-image:" + img + ";}@media (max-width:1024px){.hm-n-g{background-size:contain;}}"},
		{"invalid grad at a breakpoint falls back", map[string]map[string]string{"base": {"bgimg": "/media/a.jpg", "grad": "nope"}},
			".hm-n-g{background-image:" + img + ";}"},
		{"both invalid", map[string]map[string]string{"base": {"bgimg": "/media/../a.jpg", "grad": "nope"}}, ""},
		{"hero", map[string]map[string]string{"base": {"bgimg": "/media/a.jpg", "grad": "linear-180-000-accent", "bgpos": "center", "bgsize": "cover", "bgrep": "no-repeat", "minh": "50vh"}},
			".hm-n-g{background-image:" + gradA + "," + img + ";background-position:center;background-repeat:no-repeat;background-size:cover;min-height:50vh;}"},
	}
	for _, c := range cases {
		if got := NodeCSS(nodeWith(c.s, false)); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
		if err := (&Doc{V: 2, Nodes: []*Node{nodeWith(c.s, false)}}).Validate(); err != nil && c.name != "invalid grad at a breakpoint falls back" && c.name != "both invalid" {
			t.Errorf("%s: Validate: %v", c.name, err)
		}
	}
	// A preset node doubles the selector on every rule, including the composed one.
	got := NodeCSS(nodeWith(map[string]map[string]string{"base": {"grad": "linear-180-000-accent", "bgimg": "/media/a.jpg"}, "sm": {"hide": "1"}}, true))
	want := ".hm-n-g.hm-n-g{background-image:" + gradA + "," + img + ";}@media (max-width:640px){.hm-n-g.hm-n-g{display:none;}}"
	if got != want {
		t.Errorf("preset node:\n got  %s\n want %s", got, want)
	}
	// A preset compiles through the same path.
	if got := PresetCSS("hero", Preset{Label: "Hero", S: map[string]map[string]string{"base": {"grad": "radial-fff-primary", "bgimg": "/media/a.jpg"}}}, CSSOpts{}); got != ".hm-p-hero{background-image:"+radial+","+img+";}" {
		t.Errorf("PresetCSS: %s", got)
	}
}

// Every longhand is emitted after the shorthand that would reset it.
func TestV2LonghandAfterShorthand(t *testing.T) {
	pairs := []struct {
		shorthand map[string]string
		longhand  map[string]string
		sp, lp    string
	}{
		{bp{"gap": "space-1"}, bp{"cgap": "space-2"}, "gap:", "column-gap:"},
		{bp{"gap": "space-1"}, bp{"rgap": "space-2"}, "gap:", "row-gap:"},
		{bp{"span": "2"}, bp{"cs": "3"}, "grid-column:", "grid-column-start:"},
		{bp{"radius": "md"}, bp{"rtl": "sm"}, "border-radius:", "border-top-left-radius:"},
		{bp{"radius": "md"}, bp{"rtr": "sm"}, "border-radius:", "border-top-right-radius:"},
		{bp{"radius": "md"}, bp{"rbr": "sm"}, "border-radius:", "border-bottom-right-radius:"},
		{bp{"radius": "md"}, bp{"rbl": "sm"}, "border-radius:", "border-bottom-left-radius:"},
		{bp{"bw": "1"}, bp{"bwt": "2"}, "border-width:", "border-top-width:"},
		{bp{"bw": "1"}, bp{"bwr": "2"}, "border-width:", "border-right-width:"},
		{bp{"bw": "1"}, bp{"bwb": "2"}, "border-width:", "border-bottom-width:"},
		{bp{"bw": "1"}, bp{"bwl": "2"}, "border-width:", "border-left-width:"},
		{bp{"cols": "2"}, bp{"gtc": "1fr 1fr"}, "grid-template-columns:repeat", "grid-template-columns:minmax"},
	}
	for _, p := range pairs {
		s := bp{}
		for k, v := range p.shorthand {
			s[k] = v
		}
		for k, v := range p.longhand {
			s[k] = v
		}
		css := NodeCSS(nodeWith(map[string]map[string]string{"base": s}, false))
		si, li := strings.Index(css, p.sp), strings.Index(css, p.lp)
		if si < 0 || li < 0 || si > li {
			t.Errorf("%s then %s: %s", p.sp, p.lp, css)
		}
	}
}

func TestV2PaddingStaysUnsigned(t *testing.T) {
	for _, k := range []string{"pt", "pb", "pl", "pr", "gap", "rgap", "cgap"} {
		for _, v := range []string{"auto", "-1rem", "-0", "-5px"} {
			if _, err := styleDecl(k, v); err == nil {
				t.Errorf("%s=%q accepted", k, v)
			}
		}
	}
	if lenRe.String() != `^(0|[0-9]{1,4}(\.[0-9]{1,2})?(px|rem|em|%|vh|vw))$` {
		t.Error("lenRe was widened")
	}
}

func TestV2OrderCount(t *testing.T) {
	for v, ok := range map[string]bool{"-1": true, "0": true, "12": true, "13": false, "-2": false, "-0": false, "01": false} {
		if _, err := styleDecl("order", v); (err == nil) != ok {
			t.Errorf("order=%q ok=%v err=%v", v, ok, err)
		}
	}
	for v, ok := range map[string]bool{"0": true, "100": true, "101": false, "-1": false} {
		if _, err := styleDecl("z", v); (err == nil) != ok {
			t.Errorf("z=%q ok=%v err=%v", v, ok, err)
		}
	}
}

func TestV2NewEnumValues(t *testing.T) {
	for _, c := range [][3]string{{"ta", "justify", "text-align:justify"}, {"dir", "row-reverse", "flex-direction:row-reverse"}, {"dir", "column-reverse", "flex-direction:column-reverse"}} {
		ds, err := styleDecl(c[0], c[1])
		if err != nil || ds[0].prop+":"+ds[0].val != c[2] {
			t.Errorf("%v: %v %v", c, ds, err)
		}
	}
}

func TestV2FontTokens(t *testing.T) {
	for v, want := range map[string]string{
		"body":    "font-family:var(--hm-font,inherit);",
		"heading": "font-family:var(--hm-font-heading,var(--hm-font,inherit));",
		"mono":    "font-family:var(--hm-font-mono,ui-monospace,SFMono-Regular,Menlo,Consolas,monospace);",
	} {
		if got := NodeCSS(styled("base", "ff", v).Nodes[0]); got != ".hm-n-a{"+want+"}" {
			t.Errorf("ff=%s: %s", v, got)
		}
	}
}

// Every pattern the table serves must mean the same in JavaScript.
func TestV2PatternsPortable(t *testing.T) {
	for _, e := range StyleSpecTable() {
		if e.Pattern == "" {
			continue
		}
		for _, bad := range []string{"(?", `\z`, `\A`, `\Q`, `\E`, `\p`, `\P`, "[[:"} {
			if strings.Contains(e.Pattern, bad) {
				t.Errorf("%s pattern %s uses %q, not portable to JS", e.Key, e.Pattern, bad)
			}
		}
		if !strings.HasPrefix(e.Pattern, "^") || !strings.HasSuffix(e.Pattern, "$") {
			t.Errorf("%s pattern is not anchored: %s", e.Key, e.Pattern)
		}
	}
}

func TestV2EveryPatternKeyHasPattern(t *testing.T) {
	for _, e := range StyleSpecTable() {
		switch e.Kind {
		case "lit", "media", "grad", "gtc", "space", "len", "fs", "radius", "color":
			if e.Pattern == "" {
				t.Errorf("%s (%s) has no pattern", e.Key, e.Kind)
			}
		}
	}
}

func TestV2SettleKeys(t *testing.T) {
	want := map[string]bool{"display": true, "hide": true, "cols": true, "gtc": true, "grad": true, "bgimg": true}
	// The E2 transform, filter and transition keys compose one declaration.
	for _, k := range []string{"tx", "ty", "sc", "rot", "skx", "sky", "blur", "bright", "contrast", "sat", "gray", "hue", "bblur", "trans", "tdur", "tease", "tdelay"} {
		want[k] = true
	}
	for _, e := range StyleSpecTable() {
		if e.Settle != want[e.Key] {
			t.Errorf("%s settle=%v", e.Key, e.Settle)
		}
	}
}

var noPunct = regexp.MustCompile("[;{}\"'\\\\<>]|/\\*")

func FuzzStyleDecl(f *testing.F) {
	keys := make([]string, 0, len(styleKeys))
	for k := range styleKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var seeds []string
	seeds = append(seeds, injections...)
	for _, g := range v2Groups {
		for _, c := range g.cases {
			seeds = append(seeds, c.good...)
			seeds = append(seeds, c.bad...)
		}
	}
	seeds = append(seeds, "linear-180-000-accent", "/media/a.jpg", "1fr 2fr", `/media/a.jpg");}body{x:y`, `url("/media/a.jpg")`)
	for i, s := range seeds {
		f.Add(keys[i%len(keys)], s)
	}
	for _, k := range []string{"bgimg", "grad", "gtc", "lh", "ls", "bw", "ml", "top", "order"} {
		f.Add(k, "/media/a.jpg")
		f.Add(k, "linear-180-000-fff")
	}
	f.Fuzz(func(t *testing.T, key, v string) {
		ds, err := styleDecl(key, v)
		if err != nil {
			return
		}
		for _, d := range ds {
			val := d.val
			if styleKeys[key].kind == "media" {
				if !strings.HasPrefix(val, `url("`) || !strings.HasSuffix(val, `")`) {
					t.Fatalf("%s=%q compiled to %q", key, v, val)
				}
				val = val[len(`url("`) : len(val)-2]
			}
			if noPunct.MatchString(val) || strings.Contains(val, "..") || strings.ContainsAny(val, "\n\r\x00") {
				t.Fatalf("%s=%q compiled to forbidden %q", key, v, d.val)
			}
		}
	})
}

var urlWrap = regexp.MustCompile(`url\("[^"]*"\)`)

// FuzzStyleCompose fuzzes the one place two validated values are joined: the
// background-image of grad plus bgimg (and every other key beside them) through
// NodeCSSWith at two breakpoints. The output must keep its shape: balanced
// braces, quotes only inside url(), no comment, tag, escape or at-rule but ours.
func FuzzStyleCompose(f *testing.F) {
	for _, g := range [][2]string{
		{"linear-180-000-accent", "/media/a.jpg"}, {"radial-fff-000", ""}, {"", "/media/a/b.png"},
		{"linear-0-fff-000-accent", `/media/a.jpg");}body{x:y`}, {"radial-fff-000", `/media/a.jpg"),url("/x`},
	} {
		f.Add(g[0], g[1], g[0], g[1], "5rem")
	}
	f.Fuzz(func(t *testing.T, grad, img, grad2, img2, pt string) {
		n := &Node{ID: "a", T: "container", S: map[string]map[string]string{
			"base": {"grad": grad, "bgimg": img, "pt": pt},
			"sm":   {"grad": grad2, "bgimg": img2, "bgsize": "cover"},
		}}
		for _, o := range []CSSOpts{{}, {Vars: true}} {
			out := NodeCSSWith(n, o)
			if strings.Count(out, "{") != strings.Count(out, "}") || strings.Count(out, "@") != strings.Count(out, "@media (max-width:") {
				t.Fatalf("grad=%q img=%q: unbalanced or foreign at-rule in %q", grad, img, out)
			}
			rest := urlWrap.ReplaceAllString(out, "url()")
			if strings.Contains(rest, `"`) || strings.ContainsAny(rest, "'\\<>\n\r\x00") || strings.Contains(rest, "/*") || strings.Contains(rest, "..") {
				t.Fatalf("grad=%q img=%q compiled to forbidden %q", grad, img, out)
			}
			for _, u := range urlWrap.FindAllString(out, -1) {
				if p := u[len(`url("`) : len(u)-2]; !mediaRe.MatchString(p) {
					t.Fatalf("url wraps a non-media path %q in %q", p, out)
				}
			}
		}
	})
}

// The display the editor writes to un-hide a node (KindInfo.Display) is stored
// as a style, so the StyleSpec must accept it for every kind, pack widgets included.
func TestKindDisplayPassesStyleSpec(t *testing.T) {
	for _, k := range Kinds() {
		if _, err := styleDecl("display", k.Display); err != nil {
			t.Errorf("%s: natural display %q is refused by the display enum: %v", k.Kind, k.Display, err)
		}
	}
}
