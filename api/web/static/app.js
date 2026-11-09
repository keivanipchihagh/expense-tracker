// Small behaviours the server can't do: the browser knows its own time zone,
// and the editor dialog closes without a round trip.
(function () {
  "use strict";

  function pad(n) {
    return String(n).padStart(2, "0");
  }

  // New expense forms arrive with empty time fields. Fill them with the
  // current local time and the browser's UTC offset in minutes east of UTC.
  function fillTimeFields(root) {
    root.querySelectorAll('input[name="tz_offset"]').forEach(function (input) {
      if (input.value === "") {
        input.value = String(-new Date().getTimezoneOffset());
      }
    });
    root.querySelectorAll('input[name="occurred_local"]').forEach(function (input) {
      if (input.value === "") {
        var d = new Date();
        input.value =
          d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) +
          "T" + pad(d.getHours()) + ":" + pad(d.getMinutes()) + ":" + pad(d.getSeconds());
      }
    });
  }

  function localDate(d) {
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate());
  }

  // Quick dates ("Today", "Yesterday", "2 days ago") set the date of the
  // When field and keep its time. The button matching the date is pressed.
  function syncQuickDates(root) {
    root.querySelectorAll(".quick-dates").forEach(function (group) {
      var input = group.closest("form").querySelector('input[name="occurred_local"]');
      var date = input.value.slice(0, 10);
      group.querySelectorAll("[data-days-ago]").forEach(function (button) {
        var d = new Date();
        d.setDate(d.getDate() - Number(button.dataset.daysAgo));
        button.setAttribute("aria-pressed", String(localDate(d) === date));
      });
    });
  }

  function setDaysAgo(button) {
    var form = button.closest("form");
    var input = form.querySelector('input[name="occurred_local"]');
    var now = new Date();
    var time = input.value.slice(11) || pad(now.getHours()) + ":" + pad(now.getMinutes()) + ":" + pad(now.getSeconds());
    var d = new Date();
    d.setDate(d.getDate() - Number(button.dataset.daysAgo));
    input.value = localDate(d) + "T" + time;
    syncQuickDates(form);
  }

  function closeEditor() {
    var editor = document.getElementById("editor");
    if (editor) editor.innerHTML = "";
    pendingReceipt = null;
  }

  document.addEventListener("htmx:load", function (event) {
    fillTimeFields(event.detail.elt);
    syncQuickDates(event.detail.elt);
    attachPendingReceipt(event.detail.elt);
  });

  document.addEventListener("input", function (event) {
    if (event.target.name === "occurred_local") syncQuickDates(event.target.closest("form"));
  });

  document.addEventListener("click", function (event) {
    var quick = event.target.closest("[data-days-ago]");
    if (quick) setDaysAgo(quick);
    // The close buttons, or a click on the dimmed backdrop around the dialog.
    if (event.target.closest("[data-close-editor]") || event.target.id === "editor") {
      closeEditor();
    }
  });

  // Dropdown menus (<details data-menu>: export, profile) close on a choice,
  // a click elsewhere, or Escape. Only one is open at a time.
  function closeMenus(except) {
    document.querySelectorAll("details[data-menu][open]").forEach(function (menu) {
      if (menu !== except) menu.removeAttribute("open");
    });
  }
  document.addEventListener("click", function (event) {
    var menu = event.target.closest("details[data-menu]");
    closeMenus(menu);
    if (menu && event.target.closest(".menu a")) menu.removeAttribute("open");
  });

  // Receipt scanning. The photo is shrunk to at most 1600px (JPEG) in the
  // browser, sent with the new expense form's hidden fields, and the server
  // answers with the form filled in for the user to check. The photo stays
  // on screen next to it while they do.
  var SCAN_MAX_SIDE = 1600;

  // The last scanned photo, kept so the form can send it along when the
  // expense is added (and put back if the form comes back with errors).
  var pendingReceipt = null;

  function attachPendingReceipt(root) {
    if (!pendingReceipt || !root.querySelector) return;
    var zone = root.querySelector("[data-scan].scanned");
    var field = root.querySelector("[data-receipt-field]");
    if (!zone || !field) return;
    var transfer = new DataTransfer();
    transfer.items.add(new File([pendingReceipt.blob], "receipt.jpg", { type: pendingReceipt.blob.type || "image/jpeg" }));
    field.files = transfer.files;
    showPreview(pendingReceipt.url);
  }

  function shrink(file) {
    return new Promise(function (resolve) {
      var url = URL.createObjectURL(file);
      var img = new Image();
      img.onload = function () {
        var scale = Math.min(1, SCAN_MAX_SIDE / Math.max(img.naturalWidth, img.naturalHeight));
        var canvas = document.createElement("canvas");
        canvas.width = Math.round(img.naturalWidth * scale);
        canvas.height = Math.round(img.naturalHeight * scale);
        canvas.getContext("2d").drawImage(img, 0, 0, canvas.width, canvas.height);
        URL.revokeObjectURL(url);
        canvas.toBlob(function (blob) {
          resolve(blob && blob.size < file.size ? blob : file);
        }, "image/jpeg", 0.85);
      };
      // A format the browser can't draw (e.g. HEIC outside Safari) is sent
      // as it is; the server says if it can't use it.
      img.onerror = function () {
        URL.revokeObjectURL(url);
        resolve(file);
      };
      img.src = url;
    });
  }

  function setScanText(zone, title, detail) {
    var text = zone.querySelector(".scan-text");
    text.innerHTML = "";
    var strong = document.createElement("strong");
    strong.textContent = title;
    var span = document.createElement("span");
    span.textContent = detail;
    text.append(strong, span);
  }

  function showPreview(url) {
    var img = document.querySelector("#editor [data-scan-preview]");
    if (img && url) {
      img.src = url;
      img.hidden = false;
    }
  }

  function scanReceipt(file) {
    var zone = document.querySelector("#editor [data-scan]");
    if (!zone || !file || zone.classList.contains("busy")) return;
    if (!/^image\//.test(file.type)) {
      setScanText(zone, "That's not an image", "Choose a photo of the receipt.");
      return;
    }
    var sheet = zone.closest(".sheet");
    var form = sheet.querySelector("form");
    var previewUrl = URL.createObjectURL(file);
    zone.classList.add("busy");
    sheet.classList.add("busy");
    showPreview(previewUrl);
    setScanText(zone, "Reading your receipt", "This can take up to a minute.");

    shrink(file)
      .then(function (image) {
        pendingReceipt = { blob: image, url: previewUrl };
        var body = new FormData();
        ["from", "to", "filter_category", "filter_merchant", "tz_offset"].forEach(function (name) {
          var input = form.querySelector('[name="' + name + '"]');
          if (input) body.append(name, input.value);
        });
        body.append("receipt", image, "receipt.jpg");
        return fetch("/ui/expenses/scan", { method: "POST", body: body, credentials: "same-origin", headers: { "HX-Request": "true" } });
      })
      .then(function (response) {
        if (response.status === 401) {
          window.location.href = "/login";
          return;
        }
        return response.text().then(function (html) {
          var editor = document.getElementById("editor");
          htmx.swap(editor, html, { swapStyle: "innerHTML" });
          showPreview(previewUrl);
          attachPendingReceipt(editor);
        });
      })
      .catch(function () {
        zone.classList.remove("busy");
        sheet.classList.remove("busy");
        setScanText(zone, "Couldn't send the photo", "Check your connection and try again.");
      });
  }

  // Tap the photo to see it full size while checking the fields.
  document.addEventListener("click", function (event) {
    if (event.target.matches("[data-scan-preview]")) event.target.classList.toggle("zoomed");
  });

  document.addEventListener("change", function (event) {
    if (event.target.matches("[data-scan-input]")) {
      scanReceipt(event.target.files[0]);
      event.target.value = "";
    }
  });

  // Drop a photo anywhere on the new expense sheet, or paste one.
  document.addEventListener("dragover", function (event) {
    var sheet = event.target.closest && event.target.closest(".sheet");
    if (sheet && sheet.querySelector("[data-scan]")) {
      event.preventDefault();
      sheet.classList.add("drag-over");
    }
  });
  document.addEventListener("dragleave", function (event) {
    var sheet = event.target.closest && event.target.closest(".sheet");
    if (sheet && !sheet.contains(event.relatedTarget)) sheet.classList.remove("drag-over");
  });
  document.addEventListener("drop", function (event) {
    var sheet = event.target.closest && event.target.closest(".sheet");
    if (sheet && sheet.querySelector("[data-scan]")) {
      event.preventDefault();
      sheet.classList.remove("drag-over");
      scanReceipt(event.dataTransfer.files[0]);
    }
  });
  document.addEventListener("paste", function (event) {
    if (!document.querySelector("#editor [data-scan]")) return;
    var item = Array.prototype.find.call(event.clipboardData.items, function (i) {
      return i.kind === "file" && /^image\//.test(i.type);
    });
    if (item) {
      event.preventDefault();
      scanReceipt(item.getAsFile());
    }
  });

  // Confirmation dialogs: [data-dialog-open="id"] opens one, [data-dialog-close]
  // or a click on the backdrop closes it. A dialog rendered with an error
  // (data-show-on-load) opens straight away.
  document.addEventListener("click", function (event) {
    var opener = event.target.closest("[data-dialog-open]");
    if (opener) {
      var dialog = document.getElementById(opener.dataset.dialogOpen);
      if (dialog) dialog.showModal();
    }
    if (event.target.closest("[data-dialog-close]")) event.target.closest("dialog").close();
    if (event.target.tagName === "DIALOG") event.target.close();
  });
  document.querySelectorAll("dialog[data-show-on-load]").forEach(function (dialog) {
    dialog.showModal();
  });

  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape") {
      // An enlarged receipt closes first, keeping the form.
      var zoomed = document.querySelector(".scan-thumb.zoomed");
      if (zoomed) {
        zoomed.classList.remove("zoomed");
        return;
      }
      closeEditor();
      closeMenus(null);
    }
  });
})();
