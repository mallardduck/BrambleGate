// Replaces htmx's native window.confirm() for every hx-confirm="..." with
// the themed dialog in layout.templ (#confirm-dialog) — htmx fires
// htmx:confirm before issuing any request that carries hx-confirm; calling
// preventDefault() suspends that request until we call issueRequest(true/false)
// ourselves, so no hx-confirm attribute anywhere else needs to change.
(function () {
  document.addEventListener("htmx:confirm", function (e) {
    if (!e.detail.question) return;
    e.preventDefault();

    const dialog = document.getElementById("confirm-dialog");
    if (!dialog) {
      e.detail.issueRequest(true);
      return;
    }
    document.getElementById("confirm-dialog-message").textContent = e.detail.question;
    dialog.classList.remove("hidden");

    function close(confirmed) {
      dialog.classList.add("hidden");
      okBtn.removeEventListener("click", onOk);
      cancelBtn.removeEventListener("click", onCancel);
      dialog.removeEventListener("click", onBackdrop);
      if (confirmed) e.detail.issueRequest(true);
    }
    function onOk() { close(true); }
    function onCancel() { close(false); }
    function onBackdrop(ev) { if (ev.target === dialog) close(false); }

    const okBtn = dialog.querySelector("[data-confirm-ok]");
    const cancelBtn = dialog.querySelector("[data-confirm-cancel]");
    okBtn.addEventListener("click", onOk);
    cancelBtn.addEventListener("click", onCancel);
    dialog.addEventListener("click", onBackdrop);
  });
})();