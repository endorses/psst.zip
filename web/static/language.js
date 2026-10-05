(() => {
  let preference = "system";
  try {
    const saved = localStorage.getItem("psst.language");
    if (saved === "en" || saved === "de") preference = saved;
  } catch {}
  let language = preference === "system" ? "en" : preference;
  if (preference === "system") {
    for (const value of navigator.languages || [navigator.language]) {
      const base = value.toLowerCase().split(/[-_]/)[0];
      if (base === "en" || base === "de") {
        language = base;
        break;
      }
    }
  }
  document.documentElement.dataset.language = preference;
  document.documentElement.lang = language;
})();
