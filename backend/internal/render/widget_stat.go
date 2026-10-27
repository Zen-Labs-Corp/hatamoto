package render

func init() {
	registerWidget("stat", func(w *widgetCtx) (any, error) {
		return struct{ Value, Label string }{
			Value: str(w.P, "prefix") + str(w.P, "value") + str(w.P, "suffix"),
			Label: str(w.P, "label"),
		}, nil
	})
}
