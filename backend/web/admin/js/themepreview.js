// Colours page: the font sample and corner boxes follow the form's inputs as you type.
// Values are checked first and set through the CSSOM only (no inline style, CSP-safe).

const RADIUS = /^[0-9]+(\.[0-9]+)?(px|rem|em)$/;
const FONT = /^[A-Za-z0-9 ,'"_-]{1,200}$/;

// previewValue returns the value when it is safe for the custom property, else "".
export function previewValue(prop, value) {
	const v = String(value ?? "").trim();
	if (prop.startsWith("--hm-preview-radius-")) return RADIUS.test(v) ? v : "";
	if (prop.startsWith("--hm-preview-font-")) return FONT.test(v) ? v : "";
	return "";
}

export function bindPreview(form, root) {
	for (const el of form.querySelectorAll("[data-hm-preview]")) {
		const prop = el.dataset.hmPreview;
		const apply = () => {
			const v = previewValue(prop, el.value);
			if (v) root.style.setProperty(prop, v);
			else root.style.removeProperty(prop);
		};
		el.addEventListener("input", apply);
		apply();
	}
}

if (typeof document !== "undefined") {
	for (const form of document.querySelectorAll("[data-hm-preview-root]")) bindPreview(form, form);
}
