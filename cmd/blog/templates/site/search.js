/* search.js - double-shift search across the blog.
   Press Shift twice quickly to open a search popup; the index is the site's
   atom.xml feed (titles, labels, full text). Purely client-side: the site
   stays static. */
(function () {
  "use strict";

  var scriptEl = document.querySelector("script[data-blog-root]");
  var root = scriptEl ? scriptEl.getAttribute("data-blog-root") : "";

  var entries = null;
  var overlay = null;
  var input = null;
  var list = null;
  var selected = 0;

  function loadIndex() {
    if (entries) {
      return Promise.resolve(entries);
    }
    return fetch(root + "atom.xml")
      .then(function (res) { return res.text(); })
      .then(function (text) {
        var doc = new DOMParser().parseFromString(text, "text/xml");
        entries = [].map.call(doc.getElementsByTagName("entry"), function (entry) {
          function first(tag) {
            var node = entry.getElementsByTagName(tag)[0];
            return node ? node.textContent : "";
          }
          var link = entry.getElementsByTagName("link")[0];
          var kind = "";
          var labels = [];
          [].forEach.call(entry.getElementsByTagName("category"), function (cat) {
            if (cat.getAttribute("scheme") === "urn:blogtool:kind") {
              kind = cat.getAttribute("term") || "";
            } else if (cat.getAttribute("term")) {
              labels.push(cat.getAttribute("term"));
            }
          });
          var tmp = document.createElement("div");
          tmp.innerHTML = first("content");
          return {
            title: first("title"),
            href: root + (link ? link.getAttribute("href") : ""),
            kind: kind,
            labels: labels,
            text: (tmp.textContent || "").toLowerCase()
          };
        });
        return entries;
      });
  }

  /* Rank 0 = title hit, 1 = label hit, 2 = body hit. An entry's rank is its
     weakest term (every term must match somewhere); -1 means no match. */
  function score(entry, terms) {
    var worst = 0;
    for (var i = 0; i < terms.length; i++) {
      var term = terms[i];
      var rank;
      if (entry.title.toLowerCase().indexOf(term) >= 0) {
        rank = 0;
      } else if (entry.labels.some(function (l) { return l.toLowerCase().indexOf(term) >= 0; })) {
        rank = 1;
      } else if (entry.text.indexOf(term) >= 0) {
        rank = 2;
      } else {
        return -1;
      }
      worst = Math.max(worst, rank);
    }
    return worst;
  }

  function search(query) {
    var q = query.trim().toLowerCase();
    if (!q) {
      return [];
    }
    var terms = q.split(/\s+/);
    return entries
      .map(function (e) { return { entry: e, rank: score(e, terms) }; })
      .filter(function (r) { return r.rank >= 0; })
      .sort(function (a, b) { return a.rank - b.rank; })
      .map(function (r) { return r.entry; });
  }

  function render(results) {
    list.innerHTML = "";
    selected = 0;
    results.slice(0, 20).forEach(function (entry, i) {
      var li = document.createElement("li");
      li.className = "blog-search-result" + (i === 0 ? " selected" : "");

      var kind = document.createElement("span");
      kind.className = "blog-search-kind";
      kind.textContent = entry.kind || "post";
      li.appendChild(kind);

      var title = document.createElement("span");
      title.className = "blog-search-title";
      title.textContent = entry.title;
      li.appendChild(title);

      entry.labels.forEach(function (label) {
        var chip = document.createElement("span");
        chip.className = "blog-search-label";
        chip.textContent = label;
        li.appendChild(chip);
      });

      li.addEventListener("click", function () {
        window.location.href = entry.href;
      });
      list.appendChild(li);
    });
  }

  function moveSelection(delta) {
    var items = list.children;
    if (!items.length) {
      return;
    }
    items[selected].classList.remove("selected");
    selected = (selected + delta + items.length) % items.length;
    items[selected].classList.add("selected");
    items[selected].scrollIntoView({ block: "nearest" });
  }

  function openSelection() {
    var items = list.children;
    if (items.length) {
      items[selected].click();
    }
  }

  var style = [
    ".blog-search-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.35);",
    "  display: flex; align-items: flex-start; justify-content: center; z-index: 9999; }",
    ".blog-search-box { background: #fff; border-radius: 8px; margin-top: 12vh; width: min(600px, 90vw);",
    "  box-shadow: 0 8px 40px rgba(0,0,0,0.25); overflow: hidden; font-family: monospace; }",
    ".blog-search-box input { width: 100%; box-sizing: border-box; border: none; outline: none;",
    "  padding: 0.9em 1em; font-size: 1em; font-family: monospace; border-bottom: 1px solid #eee; }",
    ".blog-search-box ul { list-style: none; margin: 0; padding: 0; max-height: 50vh; overflow-y: auto; }",
    ".blog-search-result { padding: 0.6em 1em; cursor: pointer; display: flex; align-items: baseline; gap: 0.6em; }",
    ".blog-search-result.selected, .blog-search-result:hover { background: #f2f8fb; }",
    ".blog-search-kind { font-size: 0.7em; color: #fff; background: #5a9ab5; border-radius: 3px; padding: 0.1em 0.4em; }",
    ".blog-search-title { color: #222; }",
    ".blog-search-label { font-size: 0.75em; color: #999; border: 1px solid #ddd; border-radius: 3px; padding: 0 0.3em; }",
    ".blog-search-empty { padding: 0.6em 1em; color: #999; }"
  ].join("\n");

  function ensureOverlay() {
    if (overlay) {
      return;
    }
    var styleEl = document.createElement("style");
    styleEl.textContent = style;
    document.head.appendChild(styleEl);

    overlay = document.createElement("div");
    overlay.className = "blog-search-overlay";

    var box = document.createElement("div");
    box.className = "blog-search-box";

    input = document.createElement("input");
    input.type = "text";
    input.placeholder = "search posts and ideas...";

    list = document.createElement("ul");

    box.appendChild(input);
    box.appendChild(list);
    overlay.appendChild(box);

    overlay.addEventListener("click", function (ev) {
      if (ev.target === overlay) {
        close();
      }
    });
    input.addEventListener("input", function () {
      render(search(input.value));
    });
    input.addEventListener("keydown", function (ev) {
      if (ev.key === "ArrowDown") { ev.preventDefault(); moveSelection(1); }
      else if (ev.key === "ArrowUp") { ev.preventDefault(); moveSelection(-1); }
      else if (ev.key === "Enter") { openSelection(); }
      else if (ev.key === "Escape") { close(); }
    });
  }

  function open() {
    ensureOverlay();
    loadIndex().then(function () {
      document.body.appendChild(overlay);
      input.value = "";
      list.innerHTML = "";
      input.focus();
    });
  }

  function close() {
    if (overlay && overlay.parentNode) {
      overlay.parentNode.removeChild(overlay);
    }
  }

  function isOpen() {
    return overlay && overlay.parentNode;
  }

  var lastShift = 0;
  document.addEventListener("keydown", function (ev) {
    if (ev.key === "Shift" && !ev.repeat) {
      var now = Date.now();
      if (now - lastShift < 400) {
        lastShift = 0;
        if (isOpen()) { close(); } else { open(); }
      } else {
        lastShift = now;
      }
    } else {
      lastShift = 0;
    }
  });
})();
