/*
  assets/js/reader.js — optional comfort features. The book reads fine without this file.
  Vanilla JavaScript, no libraries. It does five small things:
    1. progress line   2. highlight the current chapter in the contents
    3. keys  ] [ t     4. theme and text-size buttons (remembered in localStorage)
    5. remember the chapter you were reading
*/
(function () {
  "use strict";

  var root = document.documentElement;
  var chapters = [].slice.call(document.querySelectorAll(".chapter"));
  var links = [].slice.call(document.querySelectorAll(".toc a[data-chapter]"));
  var bar = document.getElementById("progress-bar");

  function store(key, value) { try { localStorage.setItem(key, value); } catch (e) {} }
  function load(key) { try { return localStorage.getItem(key); } catch (e) { return null; } }

  /* 1. Progress: how far down the page, as a percentage. */
  var ticking = false;
  function updateProgress() {
    var max = document.documentElement.scrollHeight - window.innerHeight;
    bar.style.width = (max > 0 ? Math.min(100, (window.scrollY / max) * 100) : 0) + "%";
    ticking = false;
  }
  window.addEventListener("scroll", function () {
    if (!ticking) { ticking = true; requestAnimationFrame(updateProgress); }
  }, { passive: true });
  updateProgress();

  /* 2. Current chapter: IntersectionObserver fires only when a chapter crosses the
        upper part of the screen, so it costs almost nothing while scrolling. */
  var current = null;
  function setCurrent(id) {
    if (id === current) return;
    current = id;
    links.forEach(function (a) {
      if (a.getAttribute("data-chapter") === id) a.setAttribute("aria-current", "true");
      else a.removeAttribute("aria-current");
    });
    store("book-chapter", id);
  }
  if ("IntersectionObserver" in window) {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) { if (e.isIntersecting) setCurrent(e.target.id); });
    }, { rootMargin: "-10% 0px -80% 0px" });
    chapters.forEach(function (c) { io.observe(c); });
  }

  /* 3. Keys. ] next, [ previous, t theme. Space / arrows / PageDown stay native.
        We ignore keys with modifiers and anything typed into a form control. */
  function currentIndex() {
    var y = window.scrollY + 120, idx = -1;
    chapters.forEach(function (c, i) { if (c.offsetTop <= y) idx = i; });
    return idx;
  }
  function go(i) {
    if (i < 0 || i >= chapters.length) return;
    chapters[i].scrollIntoView();
    history.replaceState(null, "", "#" + chapters[i].id);
  }
  document.addEventListener("keydown", function (e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    var tag = (e.target.tagName || "").toLowerCase();
    if (tag === "input" || tag === "textarea" || tag === "select") return;
    if (e.key === "]") go(currentIndex() + 1);
    else if (e.key === "[") go(Math.max(0, currentIndex() - 1));
    else if (e.key === "t") cycleTheme();
  });

  /* 4. Theme and text size. Saved values are re-applied in head.html before paint. */
  var themes = ["light", "sepia", "dark"];
  function cycleTheme() {
    var next = themes[(themes.indexOf(root.getAttribute("data-theme")) + 1) % themes.length];
    root.setAttribute("data-theme", next);
    store("book-theme", next);
  }
  function resize(delta) {
    var size = parseFloat(getComputedStyle(root).getPropertyValue("--size")) || 19;
    size = Math.max(15, Math.min(26, size + delta));
    root.style.setProperty("--size", size + "px");
    store("book-size", size);
  }
  document.getElementById("theme").addEventListener("click", cycleTheme);
  document.getElementById("size-up").addEventListener("click", function () { resize(1); });
  document.getElementById("size-down").addEventListener("click", function () { resize(-1); });

  /* 5. Resume: if the address has no #chapter, return to where you stopped. */
  if (!location.hash) {
    var last = load("book-chapter");
    var target = last && document.getElementById(last);
    if (target && chapters.indexOf(target) > 0) {
      // Defer one frame so the browser's own initial scroll does not override ours.
      requestAnimationFrame(function () { target.scrollIntoView(); });
    }
  }
})();
