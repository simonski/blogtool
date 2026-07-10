/* nav.js - keyboard navigation across the site.
   On the list pages: left/right switches between blog and ideas, up/down
   moves a highlight through the entry links, enter opens the highlighted
   entry. On a post or idea: esc goes back to its list page. Purely
   client-side: the site stays static. */
(function () {
  "use strict";

  var scriptEl = document.querySelector("script[data-blog-root]");
  var root = scriptEl ? scriptEl.getAttribute("data-blog-root") : "";

  /* List pages live at the site root (data-blog-root=""); posts and ideas
     always render one or more directories down. */
  var isListPage = root === "";
  var inIdeas = window.location.pathname.indexOf("/ideas") >= 0;

  var links = [];
  var selected = -1;

  var styleEl = document.createElement("style");
  styleEl.textContent =
    ".blog-kbd-selected { background: #f2f8fb; outline: 2px solid #5a9ab5; outline-offset: 2px; }";
  document.head.appendChild(styleEl);

  function collectLinks() {
    links = [].slice.call(document.querySelectorAll(".season a"));
  }

  function select(index) {
    if (selected >= 0 && links[selected]) {
      links[selected].classList.remove("blog-kbd-selected");
    }
    selected = index;
    if (selected >= 0 && links[selected]) {
      links[selected].classList.add("blog-kbd-selected");
      links[selected].scrollIntoView({ block: "nearest" });
    }
  }

  function move(delta) {
    collectLinks();
    if (!links.length) {
      return;
    }
    var next;
    if (selected < 0) {
      next = delta > 0 ? 0 : links.length - 1;
    } else {
      next = (selected + delta + links.length) % links.length;
    }
    select(next);
  }

  document.addEventListener("keydown", function (ev) {
    if (ev.altKey || ev.ctrlKey || ev.metaKey || ev.shiftKey) {
      return;
    }
    var target = ev.target;
    if (target && (target.tagName === "INPUT" || target.tagName === "TEXTAREA" ||
        target.tagName === "SELECT" || target.isContentEditable)) {
      return;
    }
    /* The double-shift search popup owns the keyboard while it is open. */
    if (document.querySelector(".blog-search-overlay")) {
      return;
    }

    if (isListPage) {
      if (ev.key === "ArrowLeft" && inIdeas) {
        window.location.href = "index.html";
      } else if (ev.key === "ArrowRight" && !inIdeas) {
        window.location.href = "ideas.html";
      } else if (ev.key === "ArrowDown") {
        ev.preventDefault();
        move(1);
      } else if (ev.key === "ArrowUp") {
        ev.preventDefault();
        move(-1);
      } else if (ev.key === "Enter" && selected >= 0 && links[selected]) {
        links[selected].click();
      }
    } else if (ev.key === "Escape") {
      window.location.href = root + (inIdeas ? "ideas.html" : "index.html");
    }
  });
})();
