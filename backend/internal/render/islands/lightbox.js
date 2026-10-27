// Lightbox: image links marked data-hm-lightbox open in a modal <dialog>. Without
// this script (or with a modified click) they are plain links to the file.
const URL_OK = /^(\/(?!\/)|https?:\/\/)[^\s"'<>\\]*$/;
let box, view, caption, count, say, prev, next, close, items, at, opener;

const el = (tag, cls, text) => {
  const node = document.createElement(tag);
  node.className = cls;
  if (text) node.textContent = text;
  return node;
};
const button = (label, onclick) => {
  const b = el("button", "hm-lb__btn", label);
  b.type = "button";
  b.addEventListener("click", onclick);
  return b;
};
const captionOf = (link) =>
  link.closest("figure")?.querySelector("figcaption")?.textContent ||
  link.getAttribute("aria-label") ||
  "";

const show = (i) => {
  at = (i + items.length) % items.length;
  const link = items[at];
  view.src = link.getAttribute("href");
  view.alt = link.querySelector("img")?.getAttribute("alt") || "";
  caption.textContent = captionOf(link);
  caption.hidden = !caption.textContent;
  count.textContent = items.length > 1 ? at + 1 + " / " + items.length : "";
  // screen readers hear the position, the picture and its caption, on opening and on each arrow
  say.textContent = [items.length > 1 ? "Image " + (at + 1) + " of " + items.length : "", view.alt, caption.textContent].filter(Boolean).join(". ");
  prev.hidden = next.hidden = items.length < 2;
};

const build = () => {
  box = el("dialog", "hm-lb");
  box.setAttribute("aria-label", "Image viewer");
  view = el("img", "hm-lb__img");
  caption = el("p", "hm-lb__cap");
  count = el("span", "hm-lb__n");
  count.setAttribute("aria-hidden", "true");
  say = el("p", "hm-lb__sr");
  say.id = "hm-lb-say";
  say.setAttribute("aria-live", "polite");
  box.setAttribute("aria-describedby", say.id);
  close = button("Close", () => box.close());
  prev = button("Previous", () => show(at - 1));
  next = button("Next", () => show(at + 1));
  const bar = el("div", "hm-lb__bar");
  bar.append(prev, count, next);
  const inner = el("div", "hm-lb__in");
  inner.append(close, view, caption, bar, say);
  box.append(inner);
  box.addEventListener("click", (e) => {
    if (e.target === box || e.target === inner) box.close();
  });
  box.addEventListener("close", () => {
    view.removeAttribute("src");
    if (opener?.isConnected) opener.focus();
  });
  box.addEventListener("keydown", (e) => {
    if (e.key === "ArrowRight" && items.length > 1) show(at + 1);
    else if (e.key === "ArrowLeft" && items.length > 1) show(at - 1);
    else if (e.key === "Tab") {
      const stops = [close, prev, next].filter((b) => !b.hidden);
      const edge = stops[e.shiftKey ? 0 : stops.length - 1];
      if (document.activeElement === edge) {
        e.preventDefault();
        stops[e.shiftKey ? stops.length - 1 : 0].focus();
      }
    } else return;
    if (e.key !== "Tab") e.preventDefault();
  });
  document.body.append(box);
};

document.addEventListener("click", (e) => {
  const link = e.target.closest?.("a[data-hm-lightbox]");
  if (!link || e.defaultPrevented || e.button || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  if (typeof HTMLDialogElement !== "function" || !URL_OK.test(link.getAttribute("href") || "")) return;
  e.preventDefault();
  if (!box) build();
  const group = link.closest(".hm-gallery");
  items = group ? [...group.querySelectorAll("a[data-hm-lightbox]")] : [link];
  items = items.filter((a) => URL_OK.test(a.getAttribute("href") || ""));
  opener = link;
  show(items.indexOf(link));
  box.showModal();
  close.focus();
});
