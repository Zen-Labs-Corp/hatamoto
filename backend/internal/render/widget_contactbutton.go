package render

import "html/template"

func init() {
	registerWidget("contactbutton", func(w *widgetCtx) (any, error) {
		w.Class = "hm-contactbtn hm-btn"
		channel := str(w.P, "channel")
		if channel == "" {
			channel = "whatsapp"
		}
		href, ok := contactLink(channel, str(w.P, "to"), str(w.P, "message"), contactDefaultsOf(w.View))
		if !ok {
			if !w.Canvas {
				return nil, errWidgetEmpty
			}
			href = "#" // inert on the canvas: the editor shows it, nobody follows it
		}
		return struct {
			Href  template.URL
			Label string
		}{href, contactLabel(channel, str(w.P, "label"))}, nil
	})
}
