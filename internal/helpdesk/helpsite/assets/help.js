// The help center's page behaviour: the theme switch, dates in the reader's language,
// popovers that close on click-away and Escape, and counting an article's view.
(function () {
  var d = document.documentElement;
  var lang = d.lang || "en";

  // Theme.
  var toggle = document.querySelector("[data-theme-toggle]");
  var label = function () {
    if (!toggle) return;
    var dark = d.classList.contains("dark");
    toggle.setAttribute("aria-label", toggle.getAttribute(dark ? "data-label-light" : "data-label-dark"));
  };
  label();
  if (toggle)
    toggle.addEventListener("click", function () {
      var dark = d.classList.toggle("dark");
      try {
        localStorage.setItem("od-theme", dark ? "dark" : "light");
      } catch (e) {}
      label();
    });

  // Dates. A browser without month names for the page's language writes them as numbers
  // ("2026 M10 22"), even when it says it supports it; the page's own date stays then.
  var now = new Date();
  var known = false;
  try {
    known = !/^M?\d+$/.test(new Intl.DateTimeFormat(lang, { month: "long" }).format(new Date(2000, 9, 15)));
  } catch (e) {}
  if (known) document.querySelectorAll("time[data-format]").forEach(function (el) {
    var at = new Date(el.getAttribute("datetime"));
    if (isNaN(at)) return;
    var opts =
      el.getAttribute("data-format") === "short"
        ? at.getFullYear() === now.getFullYear()
          ? { month: "short", day: "numeric" }
          : { month: "short", day: "numeric", year: "numeric" }
        : { month: "long", day: "numeric", year: "numeric" };
    try {
      var text = new Intl.DateTimeFormat(lang, opts).format(at);
      var tpl = el.getAttribute("data-tpl");
      el.textContent = tpl ? tpl.replace("{{date}}", text) : text;
      el.title = new Intl.DateTimeFormat(lang, { dateStyle: "full", timeStyle: "short" }).format(at);
    } catch (e) {}
  });

  // Popovers (<details>): one open at a time, closed by a click elsewhere or Escape.
  var pops = document.querySelectorAll("details[data-popover]");
  document.addEventListener("click", function (e) {
    pops.forEach(function (p) {
      if (p.open && !p.contains(e.target)) p.open = false;
    });
  });
  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;
    pops.forEach(function (p) {
      if (!p.open) return;
      p.open = false;
      var s = p.querySelector("summary");
      if (s) s.focus();
    });
  });

  // A chosen language is remembered for the next visit.
  document.querySelectorAll("a[data-lang]").forEach(function (a) {
    a.addEventListener("click", function () {
      try {
        localStorage.setItem("od-help-lang", a.getAttribute("data-lang"));
      } catch (e) {}
    });
  });

  // The home page's search takes the keyboard where there is one.
  var auto = document.querySelector("[data-autofocus]");
  if (auto && matchMedia("(pointer: fine)").matches) auto.focus({ preventScroll: true });

  // A view counts once per article per visit.
  var article = document.body.getAttribute("data-article");
  var views = document.body.getAttribute("data-views");
  if (article && views && navigator.sendBeacon) {
    var key = "od-help-view:" + article;
    try {
      if (sessionStorage.getItem(key)) return;
      sessionStorage.setItem(key, "1");
    } catch (e) {}
    navigator.sendBeacon(views, article);
  }
})();
