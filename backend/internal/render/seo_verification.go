package render

// verificationHead appends the webmaster verification tags to h.Meta. With no
// saved verification settings it adds nothing.
func verificationHead(h *headView, v *PageView) {
	if v.SEO == nil {
		return
	}
	for _, m := range v.SEO.Verification.Metas() {
		h.Meta = appendMeta(h.Meta, m.Name, m.Content)
	}
}
