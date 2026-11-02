// Plugins list: "/" outside a field moves focus to the page's search box.

// isSlashFocus reports whether the key event is a bare "/" outside any field.
export function isSlashFocus(e) {
	const t = e.target ?? {};
	return e.key === "/" && !e.ctrlKey && !e.metaKey && !e.altKey && !/^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName) && !t.isContentEditable;
}

if (typeof document !== "undefined") {
	document.addEventListener("keydown", (evt) => {
		const box = isSlashFocus(evt) && document.querySelector("[data-hm-slash-focus]");
		if (box) { evt.preventDefault(); box.focus(); }
	});
}
