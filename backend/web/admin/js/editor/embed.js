// Video embeds, allowlisted forms only. A pasted address is turned into the one
// iframe address the server's sanitiser accepts (YouTube and Vimeo players);
// anything else is refused, so no arbitrary iframe can be authored here.
// The server is authoritative: its allowlist defaults to these hosts and an
// operator may change it (HATAMOTO_EMBED_HOSTS); a src the server refuses is dropped.

const YOUTUBE_ID = /^[A-Za-z0-9_-]{11}$/;
const VIMEO_ID = /^\d{5,12}$/;

/** {provider, id, src, title} for an allowlisted video address, else null. */
export function parseEmbedURL(input) {
  let u;
  try {
    u = new URL(String(input ?? "").trim());
  } catch {
    return null;
  }
  if (u.protocol !== "https:" && u.protocol !== "http:") return null;
  if (u.username || u.password) return null;
  const host = u.hostname.toLowerCase().replace(/^www\./, "");
  const parts = u.pathname.split("/").filter(Boolean);
  let youtube = "";
  let vimeo = "";
  if (host === "youtube.com" || host === "m.youtube.com" || host === "youtube-nocookie.com") {
    if (parts[0] === "watch") youtube = u.searchParams.get("v") ?? "";
    else if (["embed", "shorts", "live", "v"].includes(parts[0])) youtube = parts[1] ?? "";
  } else if (host === "youtu.be") {
    youtube = parts[0] ?? "";
  } else if (host === "vimeo.com") {
    vimeo = /^\d+$/.test(parts[0] ?? "") ? parts[0] : "";
  } else if (host === "player.vimeo.com" && parts[0] === "video") {
    vimeo = parts[1] ?? "";
  }
  if (YOUTUBE_ID.test(youtube)) {
    return { provider: "youtube", id: youtube, src: `https://www.youtube-nocookie.com/embed/${youtube}`, title: "YouTube video" };
  }
  if (VIMEO_ID.test(vimeo)) {
    return { provider: "vimeo", id: vimeo, src: `https://player.vimeo.com/video/${vimeo}`, title: "Vimeo video" };
  }
  return null;
}

/** Validator for askRow: {ok, value} or {ok:false, error}. */
export function validateEmbedURL(input) {
  const e = parseEmbedURL(input);
  return e ? { ok: true, value: e.src } : { ok: false, error: "Use the address of a YouTube or Vimeo video, for example https://www.youtube.com/watch?v=…" };
}
