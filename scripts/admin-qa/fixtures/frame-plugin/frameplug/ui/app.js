hatamoto.ready.then((r) => { document.getElementById("state").textContent = "bridge ready: " + r.plugin; });
// camera.capture: show ok, error, the data URL prefix and the size in #out.
document.getElementById("snap").addEventListener("click", async () => {
  const out = document.getElementById("out");
  try {
    const r = await hatamoto.camera.capture();
    out.textContent = JSON.stringify({ ok: r.ok, error: r.error, image: r.image ? r.image.slice(0, 23) : "", length: r.image ? r.image.length : 0, width: r.width, height: r.height });
  } catch (err) {
    out.textContent = JSON.stringify({ refused: String(err.message || err) });
  }
});
