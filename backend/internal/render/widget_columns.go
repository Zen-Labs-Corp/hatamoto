package render

import "strconv"

func init() {
	registerWidget("columns", func(w *widgetCtx) (any, error) {
		w.Class = "hm-cols hm-cols--" + strconv.Itoa(intProp(w.P, "count", 2, 2, 6))
		return nil, nil
	})
}
