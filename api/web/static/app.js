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

  function closeEditor() {
    var editor = document.getElementById("editor");
    if (editor) editor.innerHTML = "";
  }

  document.addEventListener("htmx:load", function (event) {
    fillTimeFields(event.detail.elt);
  });

  document.addEventListener("click", function (event) {
    // The close buttons, or a click on the dimmed backdrop around the dialog.
    if (event.target.closest("[data-close-editor]") || event.target.id === "editor") {
      closeEditor();
    }
  });

  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape") closeEditor();
  });
})();
