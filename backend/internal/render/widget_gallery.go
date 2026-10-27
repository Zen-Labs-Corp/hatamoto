package render

func init() {
	registerWidget("gallery", func(w *widgetCtx) (any, error) {
		w.Class = "hm-gallery"
		return nil, nil
	})
}
