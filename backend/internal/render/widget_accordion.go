package render

func init() {
	registerWidget("accordion", func(w *widgetCtx) (any, error) {
		w.Class = "hm-acc"
		return nil, nil
	})
	registerWidget("accorditem", func(w *widgetCtx) (any, error) {
		w.Class = "hm-acc__i"
		x := struct {
			Title, Name string
			Open        bool
		}{Title: str(w.P, "title"), Open: w.P["open"] == true}
		// Items of a "single" accordion share a name, so the browser keeps one open.
		if p := w.Parent; p != nil && p.T == "accordion" && p.P["single"] == true {
			x.Name = "hm-acc-" + p.ID
		}
		return x, nil
	})
}
