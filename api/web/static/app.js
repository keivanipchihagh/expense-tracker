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
  }

  document.addEventListener("htmx:load", function (event) {
    fillTimeFields(event.detail.elt);
    syncQuickDates(event.detail.elt);
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
      closeEditor();
      closeMenus(null);
    }
  });
})();
