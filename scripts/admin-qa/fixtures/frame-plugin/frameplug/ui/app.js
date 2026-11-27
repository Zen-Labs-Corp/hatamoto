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
// runInputs(run, inputs): show the result of core's confirm and start in #out.
document.getElementById("startrun").addEventListener("click", async () => {
  const out = document.getElementById("out");
  try {
    out.textContent = JSON.stringify(await hatamoto.runInputs("import", { source: "https://example.com/", limit: "3" }));
  } catch (err) {
    out.textContent = JSON.stringify({ refused: String(err.message || err) });
  }
});
// grow: add 120 rows so the page is taller than the viewport (the host frame should follow).
document.getElementById("grow").addEventListener("click", () => {
  const rows = document.getElementById("rows");
  for (let i = 0; i < 120; i++) {
    const p = document.createElement("p");
    p.textContent = "Row " + (rows.children.length + 1);
    rows.append(p);
  }
});
