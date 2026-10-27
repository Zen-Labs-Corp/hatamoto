// Video: swap the play link for the embed on click; nothing is requested before.
const EMBED = /^https:\/\/(www\.youtube-nocookie\.com\/embed\/|player\.vimeo\.com\/video\/)[\w-]+$/;
document.addEventListener("click", (e) => {
  const link = e.target.closest?.(".hm-vid__b");
  if (!link || e.defaultPrevented || e.button || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  const box = link.parentNode;
  const src = box.dataset.embed;
  if (!EMBED.test(src || "")) return;
  e.preventDefault();
  const frame = document.createElement("iframe");
  frame.src = src + "?autoplay=1";
  frame.title = box.dataset.title || "Video";
  frame.allow = "autoplay; picture-in-picture; fullscreen";
  frame.allowFullscreen = true;
  frame.referrerPolicy = "strict-origin-when-cross-origin";
  box.replaceChildren(frame);
  frame.focus();
});
