// The save-conflict and unsaved-copy-recovery banners (a lazy chunk; screen.js falls back to a plain line).
// They float over the top of the canvas, so showing one never moves the page. Each is tagged data-banner
// (conflict | recovery) so a successful save clears the conflict and nothing else.
import { h, actButton } from "@hm/builder/dom";

/** Puts node in the banner area as the one banner of its kind. */
function show(banner, kind, node) {
  banner.querySelector(`[data-banner="${kind}"]`)?.remove();
  node.dataset.banner = kind;
  banner.append(node);
}

/** Shows the conflict choice: keep mine (saver.resolve("overwrite")), load theirs, or look at the versions first. */
export function conflict(banner, saver, { versionsUrl = "" } = {}) {
  show(
    banner,
    "conflict",
    h(
      "div",
      { class: "hm-flash hm-flash--error", role: "alert" },
      h("span", { text: "Someone else saved this page while you were editing. Your changes are still here and are not saved yet." }),
      actButton("Keep my version", { onclick: () => saver.resolve("overwrite") }),
      actButton("Load their version", { onclick: () => (saver.leave(), location.reload()) }),
      versionsUrl && h("a", { href: versionsUrl, target: "_blank", rel: "noopener", text: "Compare in Versions" }),
    ),
  );
}

/** Offers the copy the server kept (saver.restorable()); restore() applies it and returns false when it is unreadable. */
export function recovery(banner, saver, found, restore) {
  const stale = found.action === "stale";
  const node = h("div", { class: "hm-flash hm-flash--info", role: "status" });
  const close = () => node.remove();
  const dismiss = () => (saver.discardLocal(), close());
  node.append(
    h("span", { text: stale ? "Unsaved changes from an older version of this page were kept for you." : "Unsaved changes from your last visit were kept for you." }),
    !stale && actButton("Restore my unsaved changes", { onclick: () => (restore() ? close() : dismiss()) }),
    actButton("Discard them", { onclick: dismiss }),
  );
  show(banner, "recovery", node);
}

/** A note from the server (not published, slug taken): stays until it is dismissed, or the next save clears it (screen.js). */
export function note(banner, text) {
  const node = h("div", { class: "hm-flash hm-flash--info", role: "status" }, h("span", { text }));
  const before = document.activeElement; // where the person was when the note came up
  const dismiss = () => {
    const had = node.contains?.(document.activeElement);
    node.remove();
    if (!had) return;
    // Back to where they were; if that is gone, the canvas region (made focusable for this), never <body>.
    const to = before && before !== document.body && before.isConnected !== false && !node.contains?.(before) ? before : banner.parentElement;
    if (to && !to.matches?.("a[href],button,input,select,textarea,[tabindex]")) to.setAttribute("tabindex", "-1");
    to?.focus?.();
  };
  node.append(actButton("Dismiss", { onclick: dismiss }));
  show(banner, "note", node);
}
