package render

func init() {
	registerWidget("buttons", func(w *widgetCtx) (any, error) {
		w.Class = "hm-buttons"
		return nil, nil
	})
}
