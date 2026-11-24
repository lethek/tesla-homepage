(function () {
  "use strict";

  // Escape follows the page's data-back link: "Back to homepage", or Cancel
  // on a confirmation page.
  // In Edit mode it cancels, asking first if the Draft has changes. An open
  // dialog takes Escape for itself.
  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape" || e.defaultPrevented || document.querySelector("dialog[open]")) return;
    var back = document.querySelector("a[data-back]");
    var cancel = document.querySelector("form[action='/edit/cancel']");
    var editor = document.getElementById("editor");
    // Claim the key, or the browser treats Escape as "stop loading" and can
    // cancel the navigation started here.
    if (back) {
      e.preventDefault();
      location.href = back.href;
    } else if (cancel) {
      e.preventDefault();
      cancel.requestSubmit(); // runs the confirmation below
    }
  });

  // Cancelling, by button or Escape, asks first if the Draft has changes.
  document.addEventListener("submit", function (e) {
    if (e.target.getAttribute("action") !== "/edit/cancel") return;
    var editor = document.getElementById("editor");
    if (editor && editor.dataset.dirty === "true" &&
        !confirm("Discard your unsaved changes and leave Edit mode?")) {
      e.preventDefault();
      return;
    }
    // The server discards a changed Draft only if this is the state the page
    // last saw, so a change made in another tab isn't thrown away unseen.
    e.target.elements.confirm_rev.value = editor ? editor.dataset.rev : "";
  });

  // An error inside an opened form may be below the fold after the page reloads.
  var formError = document.querySelector(".setting-form [role=alert]");
  if (formError) formError.scrollIntoView({ block: "center" });

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
      // back into the dialog, so leave it open then. Other requests from
      // inside the dialog, such as library searches, never close it.
      if (e.detail.elt.classList.contains("tile-form") && e.detail.successful &&
          !e.detail.xhr.getResponseHeader("HX-Retarget")) {
        dialog.close();
      }
    });
    dialog.addEventListener("click", function (e) {
      if (e.target.closest("[data-close]")) dialog.close();
    });

    // Tile form: icon source tabs, Plate defaults and library search.
    dialog.addEventListener("change", function (e) {
      var form = e.target.closest(".tile-form");
      if (!form) return;
      var t = e.target;
      if (t.name === "source") {
        form.dataset.source = t.value;
      } else if (t.name === "url") {
        searchLibrary(form);
      }
    });
    dialog.addEventListener("input", function (e) {
      var form = e.target.closest(".tile-form");
      if (!form) return;
      var t = e.target;
      if (t.name === "padding") {
        t.nextElementSibling.textContent = t.value + "%";
      } else if (t.name === "plate_custom") {
        form.querySelector("#plate-custom").checked = true;
      } else if (t.name === "q") {
        form.dataset.queryTouched = "1";
      } else if (t.name === "title" && !form.dataset.queryTouched) {
        var q = form.querySelector("[name=q]");
        if (q) q.value = t.value;
        clearTimeout(form._searchTimer);
        form._searchTimer = setTimeout(function () { searchLibrary(form); }, 300);
      }
    });
  }

  // Icon library page: custom icon dialogs.
  document.addEventListener("click", function (e) {
    var open = e.target.closest("[data-open]");
    if (open) {
      document.getElementById(open.dataset.open).showModal();
    } else if (e.target.closest("[data-close-dialog]")) {
      e.target.closest("dialog").close();
    }
  });

  // Custom icon forms on the Icon library page: image source tabs.
  document.addEventListener("change", function (e) {
    var form = e.target.closest(".custom-form");
    if (form && e.target.name === "source") form.dataset.source = e.target.value;
  });

  // A search response can arrive after the user has tapped an icon, for example
  // when tapping blurs the address field and starts a search. Keep the user's
  // pick when the results are replaced.
  document.body.addEventListener("htmx:beforeSwap", function (e) {
    if (e.detail.target.id !== "library-results") return;
    var picked = e.detail.target.querySelector(".lib-radio:checked");
    e.detail.target._pick = picked ? picked.value : "";
  });
  document.body.addEventListener("htmx:afterSwap", function (e) {
    var results = e.detail.target;
    if (results.id !== "library-results" || !results._pick) return;
    var radios = Array.prototype.slice.call(results.querySelectorAll(".lib-radio"));
    var keep = radios.find(function (r) { return r.value === results._pick; });
    if (!keep) {
      // The pick isn't in the new results, so keep it as a hidden choice.
      keep = document.createElement("input");
      keep.type = "radio";
      keep.name = "library";
      keep.value = results._pick;
      keep.className = "lib-radio";
      keep.hidden = true;
      results.prepend(keep);
    }
    keep.checked = true;
  });

  function searchLibrary(form) {
    var q = form.querySelector("[name=q]");
    if (q && window.htmx) htmx.trigger(q, "search");
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
      // The Draft this page is showing, so the server can refuse if it changed.
      headers: { "Content-Type": "application/json", "X-Draft-Rev": document.getElementById("editor").dataset.rev },
      body: JSON.stringify(order)
    }).then(function (r) {
      if (!r.ok) location.reload();
      else {
        var editor = document.getElementById("editor");
        editor.dataset.dirty = "true";
        editor.dataset.rev = r.headers.get("X-Draft-Rev") || editor.dataset.rev;
      }
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

  // Drop zones: a file input inside a label, so clicking it opens the file
  // chooser. Dropping a file onto it fills the input. Listeners sit on the
  // document so zones loaded later by htmx work too.
  function zoneOf(e) { return e.target.closest && e.target.closest(".dropzone"); }
  function hasFiles(e) { return e.dataTransfer && Array.prototype.indexOf.call(e.dataTransfer.types, "Files") >= 0; }
  ["dragenter", "dragover"].forEach(function (type) {
    document.addEventListener(type, function (e) {
      if (!hasFiles(e)) return;
      // Stop a file dropped outside a zone from replacing the page.
      e.preventDefault();
      var zone = zoneOf(e);
      e.dataTransfer.dropEffect = zone ? "copy" : "none";
      if (zone) zone.classList.add("dragging");
    });
  });
  document.addEventListener("dragleave", function (e) {
    var zone = zoneOf(e);
    if (zone && !zone.contains(e.relatedTarget)) zone.classList.remove("dragging");
  });
  document.addEventListener("drop", function (e) {
    if (!hasFiles(e)) return;
    e.preventDefault();
    var zone = zoneOf(e);
    if (!zone) return;
    zone.classList.remove("dragging");
    var input = zone.querySelector("input[type=file]");
    var dt = new DataTransfer();
    dt.items.add(e.dataTransfer.files[0]);
    input.files = dt.files;
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
  document.addEventListener("change", function (e) {
    var zone = zoneOf(e);
    if (!zone || e.target.type !== "file") return;
    var file = e.target.files[0];
    zone.querySelector("[data-dropzone-name]").textContent = file ? file.name : "";
    zone.classList.toggle("has-file", !!file);
  });
})();
