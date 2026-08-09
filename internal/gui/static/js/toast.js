// Auto-dismiss and manual close for error toasts. htmx does the DOM
// insertion (hx-swap-oob into #toast-container, see internal/gui/ui/toast.templ);
// this file only watches that container for newly added toasts.
(function () {
  const AUTO_DISMISS_MS = 6000;

  function dismiss(toast) {
    if (!toast) return;
    toast.classList.add("opacity-0");
    setTimeout(() => toast.remove(), 200);
  }

  document.addEventListener("click", function (e) {
    const btn = e.target.closest("[data-toast-close]");
    if (btn) dismiss(btn.closest("[data-toast]"));
  });

  document.addEventListener("DOMContentLoaded", function () {
    const container = document.getElementById("toast-container");
    if (!container) return;
    new MutationObserver(function (mutations) {
      for (const m of mutations) {
        m.addedNodes.forEach(function (node) {
          if (node.nodeType === 1 && node.hasAttribute("data-toast")) {
            setTimeout(() => dismiss(node), AUTO_DISMISS_MS);
          }
        });
      }
    }).observe(container, { childList: true });
  });
})();