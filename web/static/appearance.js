// Apply the saved appearance before styles render to avoid a theme flash.
// Keep this external: production permits same-origin scripts and build hashes,
// never arbitrary inline JavaScript.
try {
  const appearance = localStorage.getItem("psst.theme");
  document.documentElement.dataset.theme =
    appearance === "light" || appearance === "dark" ? appearance : "system";
} catch {
  document.documentElement.dataset.theme = "system";
}
