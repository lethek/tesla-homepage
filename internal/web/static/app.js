(function () {
  "use strict";

  // Clock
  var clock = document.querySelector("[data-clock]");
  function tick() {
    if (!clock) return;
    clock.textContent = new Date().toLocaleString(undefined, {
      weekday: "long", year: "numeric", month: "long", day: "numeric",
      hour: "numeric", minute: "2-digit", second: "2-digit"
    });
  }
  tick();
  setInterval(tick, 1000);

  // Fullscreen button, shown only where the browser supports it.
  var fs = document.querySelector("[data-fullscreen]");
  if (fs && document.fullscreenEnabled) {
    fs.hidden = false;
    fs.addEventListener("click", function () {
      if (document.fullscreenElement) document.exitFullscreen();
      else document.documentElement.requestFullscreen();
    });
  }

  // Close the menu when tapping elsewhere.
  document.addEventListener("click", function (e) {
    document.querySelectorAll("details.menu[open]").forEach(function (d) {
      if (!d.contains(e.target)) d.open = false;
    });
  });

  // Tile dialog
  var dialog = document.getElementById("dialog");
  if (dialog) {
    document.body.addEventListener("htmx:afterSwap", function (e) {
      if (e.detail.target.id === "dialog-body" && !dialog.open) dialog.showModal();
    });
    document.body.addEventListener("htmx:afterRequest", function (e) {
      // A successful save replaces the editor; a form error is re-targeted
      // back into the dialog, so leave it open then.
      if (dialog.contains(e.detail.elt) && e.detail.successful &&
          !e.detail.xhr.getResponseHeader("HX-Retarget")) {
        dialog.close();
      }
    });
    dialog.addEventListener("click", function (e) {
      if (e.target.closest("[data-close]")) dialog.close();
    });
  }

  // Drag and drop in edit mode
  function saveOrder() {
    var order = Array.prototype.map.call(document.querySelectorAll("#editor .section"), function (s) {
      return {
        id: s.dataset.id,
        tiles: Array.prototype.map.call(s.querySelectorAll(".tile[data-id]"), function (t) { return t.dataset.id; })
      };
    });
    fetch("/edit/order", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(order)
    }).then(function (r) {
      if (!r.ok) location.reload();
    }, function () {
      location.reload();
    });
  }

  if (window.htmx && window.Sortable) {
    htmx.onLoad(function (root) {
      root.querySelectorAll("[data-sortable-sections]").forEach(function (el) {
        Sortable.create(el, { handle: ".handle", draggable: ".section", animation: 150, onEnd: saveOrder });
      });
      root.querySelectorAll("[data-sortable-tiles]").forEach(function (el) {
        Sortable.create(el, {
          group: "tiles", draggable: ".tile", animation: 150,
          delay: 150, delayOnTouchOnly: true,
          filter: "button", preventOnFilter: false,
          onEnd: saveOrder
        });
      });
    });
  }
})();
