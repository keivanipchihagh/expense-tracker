// Light/dark theme switch. Loaded in <head> without defer so the theme is set
// before the page paints. The page follows the system theme until the user
// picks one; picking the system's own theme goes back to following it.
(function () {
  "use strict";

  var KEY = "theme";
  var root = document.documentElement;
  var media = window.matchMedia("(prefers-color-scheme: dark)");

  function systemTheme() {
    return media.matches ? "dark" : "light";
  }

  function stored() {
    try {
      var value = localStorage.getItem(KEY);
      return value === "light" || value === "dark" ? value : null;
    } catch (e) {
      return null;
    }
  }

  function store(theme) {
    try {
      if (theme === systemTheme()) localStorage.removeItem(KEY);
      else localStorage.setItem(KEY, theme);
    } catch (e) {
      // Storage blocked: the choice lasts until the page is left.
    }
  }

  function sync() {
    var dark = root.dataset.theme === "dark";
    document.querySelectorAll("[data-theme-toggle]").forEach(function (button) {
      button.setAttribute("aria-checked", String(dark));
    });
  }

  function apply(theme) {
    root.dataset.theme = theme;
    sync();
  }

  apply(stored() || systemTheme());

  media.addEventListener("change", function () {
    if (!stored()) apply(systemTheme());
  });

  document.addEventListener("DOMContentLoaded", sync);

  document.addEventListener("click", function (event) {
    if (!event.target.closest("[data-theme-toggle]")) return;
    var next = root.dataset.theme === "dark" ? "light" : "dark";
    store(next);
    apply(next);
  });
})();
