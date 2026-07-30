(() => {
  "use strict";

  document.addEventListener("htmx:configRequest", (event) => {
    const meta = document.querySelector('meta[name="csrf-token"]');
    if (meta?.content) {
      event.detail.headers["X-CSRF-Token"] = meta.content;
    }
  });

  document.addEventListener("htmx:beforeRequest", () => {
    const active = document.activeElement;
    if (active?.id) {
      document.documentElement.dataset.godjangoFocus = active.id;
    }
  });

  document.addEventListener("htmx:beforeSwap", (event) => {
    if (event.detail.xhr.status === 422) {
      event.detail.shouldSwap = true;
      event.detail.isError = false;
    }
  });

  document.addEventListener("htmx:afterSwap", (event) => {
    const explicit = event.detail.elt.querySelector?.("[autofocus]");
    const priorID = document.documentElement.dataset.godjangoFocus;
    const prior = priorID ? document.getElementById(priorID) : null;
    const target = explicit || prior || event.detail.elt;
    if (target instanceof HTMLElement) {
      target.focus({ preventScroll: false });
    }
    delete document.documentElement.dataset.godjangoFocus;
  });
})();
