// Light/dark/system theme toggle. Applied before first paint by an inline
// script in layout.templ (avoids a light->dark flash); this file wires the
// three toggle buttons after the DOM loads.
(function () {
  const KEY = "bramble-theme";

  function apply(theme) {
    document.documentElement.setAttribute("data-theme", theme);
    document.querySelectorAll("[data-theme-btn]").forEach((btn) => {
      btn.setAttribute("aria-pressed", String(btn.getAttribute("data-theme-btn") === theme));
    });
  }

  document.addEventListener("DOMContentLoaded", function () {
    apply(localStorage.getItem(KEY) || "system");
    document.querySelectorAll("[data-theme-btn]").forEach((btn) => {
      btn.addEventListener("click", function () {
        const theme = btn.getAttribute("data-theme-btn");
        localStorage.setItem(KEY, theme);
        apply(theme);
      });
    });
  });
})();
