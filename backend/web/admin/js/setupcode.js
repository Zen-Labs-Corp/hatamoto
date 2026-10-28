// Prefills the setup code from the URL fragment, then drops the fragment. Progressive enhancement: the form works without JS.
const m = /^#code=(.+)$/.exec(location.hash);
if (m) {
  const el = document.getElementById("code");
  let v = "";
  try {
    v = decodeURIComponent(m[1]);
  } catch (_) {
    v = "";
  }
  if (el && !el.disabled && !el.value && v) el.value = v;
  history.replaceState(null, "", location.pathname + location.search);
}
