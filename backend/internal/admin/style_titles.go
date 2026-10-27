package admin

import "hatamoto.dev/core/internal/builder"

// styleKeyList is every style key of the builder vocabulary in the order the
// presets page offers them, with its plain-language title. The titles name the
// keys in version diffs and on the presets page; TestEveryStyleKeyHasTitle ties
// the list to builder.StyleSpecTable so a new key cannot ship without one.
var styleKeyList = []struct{ key, title string }{
	{"pt", "Padding top"}, {"pb", "Padding bottom"}, {"pl", "Padding left"}, {"pr", "Padding right"},
	{"mt", "Margin top"}, {"mb", "Margin bottom"}, {"ml", "Margin left"}, {"mr", "Margin right"},
	{"gap", "Gap"}, {"rgap", "Row gap"}, {"cgap", "Column gap"},
	{"bg", "Background"}, {"color", "Text colour"}, {"radius", "Corners"}, {"shadow", "Shadow"}, {"fs", "Font size"},
	{"dir", "Direction"}, {"ai", "Align items"}, {"jc", "Justify"}, {"ac", "Align content"}, {"wrap", "Wrap"}, {"ta", "Text align"}, {"as", "Align self"}, {"js", "Justify self"},
	{"display", "Display"}, {"cols", "Columns"}, {"gtc", "Column tracks"}, {"rows", "Rows"}, {"span", "Column span"}, {"cs", "Column start"}, {"rs", "Row span"}, {"order", "Order"},
	{"maxw", "Max width"}, {"w", "Width"}, {"minw", "Min width"}, {"minh", "Min height"}, {"h", "Height"}, {"maxh", "Max height"},
	{"aspect", "Aspect ratio"}, {"fit", "Image fit"}, {"objpos", "Image position"}, {"overflow", "Overflow"},
	{"pos", "Position"}, {"top", "Offset top"}, {"right", "Offset right"}, {"bottom", "Offset bottom"}, {"left", "Offset left"}, {"z", "Stack order"}, {"scrollmt", "Scroll margin top"},
	{"ff", "Font"}, {"fw", "Font weight"}, {"lh", "Line height"}, {"ls", "Letter spacing"}, {"tt", "Text case"}, {"td", "Text decoration"}, {"fst", "Font style"},
	{"bw", "Border width"}, {"bwt", "Border width top"}, {"bwr", "Border width right"}, {"bwb", "Border width bottom"}, {"bwl", "Border width left"},
	{"bs", "Border style"}, {"bc", "Border colour"},
	{"rtl", "Corner top left"}, {"rtr", "Corner top right"}, {"rbr", "Corner bottom right"}, {"rbl", "Corner bottom left"},
	{"bgimg", "Background image"}, {"grad", "Gradient"}, {"bgpos", "Background position"}, {"bgsize", "Background size"}, {"bgrep", "Background repeat"}, {"bgatt", "Background attachment"},
	{"op", "Opacity"}, {"hide", "Hide on"},
	{"tx", "Move X"}, {"ty", "Move Y"}, {"sc", "Scale"}, {"rot", "Rotate"}, {"skx", "Skew X"}, {"sky", "Skew Y"}, {"torigin", "Transform origin"},
	{"blur", "Blur"}, {"bright", "Brightness"}, {"contrast", "Contrast"}, {"sat", "Saturation"}, {"gray", "Grayscale"}, {"hue", "Hue rotate"},
	{"bblur", "Backdrop blur"}, {"blend", "Blend mode"},
	{"trans", "Transition"}, {"tdur", "Transition duration"}, {"tease", "Transition easing"}, {"tdelay", "Transition delay"},
}

// styleKeyTitles maps a style key to its title.
var styleKeyTitles = func() map[string]string {
	m := make(map[string]string, len(styleKeyList))
	for _, k := range styleKeyList {
		m[k.key] = k.title
	}
	return m
}()

// styleKeyTitle is the title of key, or the key itself when it has none.
func styleKeyTitle(key string) string {
	if t := styleKeyTitles[key]; t != "" {
		return t
	}
	return key
}

// breakpointTitles and stateTitles name the parts of a style-map key.
var (
	breakpointTitles = map[string]string{"base": "Desktop", "md": "Tablet", "sm": "Phone"}
	stateTitles      = map[string]string{"hover": "Hover", "focus": "Focus", "active": "Pressed", "parent-hover": "Parent hover"}
)

// breakpointTitle names a style-map key: "Tablet" for md, "Hover, Tablet" for
// md:hover. An unknown key is shown as it is.
func breakpointTitle(key string) string {
	bp, state, ok := builder.ParseSKey(key)
	if !ok {
		return key
	}
	if state == "" {
		return breakpointTitles[bp]
	}
	return stateTitles[state] + ", " + breakpointTitles[bp]
}
