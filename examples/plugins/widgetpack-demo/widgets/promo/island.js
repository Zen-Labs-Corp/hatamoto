// Adds a "Dismiss" button to every promo banner; it hides the banner for the visit.
for (const box of document.querySelectorAll(".hm-widgetpack-demo_promo")) {
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "d";
  btn.textContent = "Dismiss";
  btn.addEventListener("click", () => {
    box.hidden = true;
  });
  box.append(btn);
}
