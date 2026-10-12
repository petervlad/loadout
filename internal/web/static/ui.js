// Small conveniences for the loadout pages. Every page works without this
// script: it only adds Copy buttons, hides Save buttons until something
// changed, submits view pickers on change, folds the sidebar's projects,
// puts diagram frames on preview pages, opens the wiki section a link
// names, filters member lists as you type and closes popovers with Escape.
"use strict";

document.documentElement.classList.add("js");

document.addEventListener("DOMContentLoaded", function () {
  var live = document.getElementById("live");

  function say(text) {
    if (live) {
      live.textContent = "";
      window.setTimeout(function () { live.textContent = text; }, 50);
    }
  }

  // Copy buttons: data-copy holds the id of the element with the text.
  document.querySelectorAll("button[data-copy]").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var src = document.getElementById(btn.getAttribute("data-copy"));
      if (!src) { return; }
      var text = src.textContent.trim();
      var done = function () {
        var label = btn.textContent;
        btn.textContent = "Copied";
        say("Copied to the clipboard");
        window.setTimeout(function () { btn.textContent = label; }, 1500);
      };
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(done, function () { selectText(src); });
      } else {
        selectText(src);
      }
    });
  });

  function selectText(el) {
    var range = document.createRange();
    range.selectNodeContents(el);
    var sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
    say("Selected; press Command-C or Control-C to copy");
  }

  // Forms marked data-dirty show their Save button only after a change.
  document.querySelectorAll("form[data-dirty]").forEach(function (form) {
    var btn = form.querySelector("[data-save]");
    if (!btn) { return; }
    btn.hidden = true;
    form.addEventListener("change", function () { btn.hidden = false; });
  });

  // Pickers that only change what the page shows submit on change. Arrow
  // keys on a closed picker change it one step at a time, so after a key
  // the change waits for Enter or for focus to leave.
  document.querySelectorAll("select[data-autosubmit]").forEach(function (sel) {
    var keyed = false, start = sel.value;
    function go() { if (sel.value !== start) { start = sel.value; sel.form.submit(); } }
    sel.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { e.preventDefault(); keyed = false; go(); return; }
      if (e.key !== "Tab") { keyed = true; }
    });
    sel.addEventListener("change", function () { if (!keyed) { go(); } });
    sel.addEventListener("blur", function () { if (keyed) { keyed = false; go(); } });
  });

  // Choosing a team's role in the invite form ticks that team: a role on
  // an unticked team would otherwise be dropped without a word.
  document.querySelectorAll("select[data-ticks]").forEach(function (sel) {
    var box = document.getElementById(sel.getAttribute("data-ticks"));
    if (!box) { return; }
    sel.addEventListener("change", function () { box.checked = true; });
  });

  // Sidebar arrows show or hide the teams of a project.
  document.querySelectorAll("button[data-toggle]").forEach(function (btn) {
    var sub = document.getElementById(btn.getAttribute("data-toggle"));
    if (!sub) { return; }
    btn.addEventListener("click", function () {
      var open = btn.getAttribute("aria-expanded") !== "true";
      btn.setAttribute("aria-expanded", open ? "true" : "false");
      sub.toggleAttribute("data-folded", !open);
    });
  });

  // Diagrams on a preview page: each figure gets its sandboxed frame, and
  // its source, which is what shows without script, folds into Diagram
  // source. The frame has an opaque origin: it can only tell its height.
  var frames = [];
  document.querySelectorAll("figure.diagram[data-frame]").forEach(function (fig) {
    var frame = document.createElement("iframe");
    frame.className = "diagram-frame";
    frame.setAttribute("sandbox", "allow-scripts");
    frame.setAttribute("loading", "lazy");
    frame.setAttribute("title", fig.getAttribute("data-title") || "Diagram");
    frame.setAttribute("height", "400");
    frame.src = fig.getAttribute("data-frame");
    fig.insertBefore(frame, fig.firstChild);
    var src = fig.querySelector(".diagram-src");
    if (src) {
      var fold = document.createElement("details");
      fold.className = "diagram-source";
      var sum = document.createElement("summary");
      sum.textContent = "Diagram source";
      fold.appendChild(sum);
      fig.replaceChild(fold, src);
      fold.appendChild(src);
    }
    frames.push(frame);
  });
  if (frames.length) {
    window.addEventListener("message", function (e) {
      var frame = null;
      for (var i = 0; i < frames.length; i++) {
        if (frames[i].contentWindow === e.source) { frame = frames[i]; }
      }
      if (!frame || !e.data || e.data.loadout !== "height") { return; }
      var h = Math.round(Number(e.data.h));
      if (!isFinite(h)) { return; }
      frame.setAttribute("height", String(Math.min(3000, Math.max(120, h))));
    });
  }

  // Wiki sections fold (details.wiki-section) and start closed. A link to
  // a heading opens the section holding it. Chrome does that itself for a
  // heading inside a section, but not for the section's own heading,
  // which sits in the summary; other browsers may do neither.
  var sections = document.querySelectorAll("details.wiki-section");
  function reveal() {
    var id = window.location.hash.slice(1);
    if (!id) { return; }
    try { id = decodeURIComponent(id); } catch (e) { return; }
    var target = document.getElementById(id);
    var section = target && target.closest("details.wiki-section");
    if (!section) { return; }
    section.open = true;
    target.scrollIntoView();
  }
  if (sections.length) {
    reveal();
    window.addEventListener("hashchange", reveal);
    // A link to the heading already in the address changes no hash.
    document.addEventListener("click", function (e) {
      var a = e.target.closest && e.target.closest("a[href^='#']");
      if (a && a.hash === window.location.hash) { reveal(); }
    });
  }

  // Open all and Close all, with two sections or more: a plain button
  // just above the first section, next to what it opens.
  if (sections.length > 1) {
    var all = document.createElement("button");
    all.type = "button";
    all.className = "btn btn-plain wiki-fold-all";
    var allOpen = function () {
      for (var i = 0; i < sections.length; i++) {
        if (!sections[i].open) { return false; }
      }
      return true;
    };
    var label = function () {
      all.textContent = allOpen() ? "Close all" : "Open all";
      var more = document.createElement("span");
      more.className = "vh";
      more.textContent = " sections";
      all.appendChild(more);
    };
    all.addEventListener("click", function () {
      var open = !allOpen();
      sections.forEach(function (s) { s.open = open; });
      label();
    });
    sections.forEach(function (s) { s.addEventListener("toggle", label); });
    label();
    sections[0].parentNode.insertBefore(all, sections[0]);
  }

  // A member search filters the lists on the page as you type. The page
  // sends every row and hides those its search did not match, so a wider
  // search can show them again. Each list's "no match" line follows it.
  document.querySelectorAll("input[data-filter]").forEach(function (input) {
    var tables = document.querySelectorAll("table[data-filterable]");
    input.addEventListener("input", function () {
      var q = input.value.trim().toLowerCase();
      tables.forEach(function (table) {
        var shown = 0;
        table.querySelectorAll("tr[data-find]").forEach(function (tr) {
          tr.hidden = q !== "" && tr.dataset.find.indexOf(q) < 0;
          if (!tr.hidden) { shown++; }
        });
        var none = table.nextElementSibling;
        if (none && none.classList.contains("filter-none")) { none.hidden = shown > 0; }
      });
    });
  });

  // A role change, a removal and the remove popover's Cancel keep the
  // search as typed, even when it was never submitted.
  var search = document.querySelector("input[data-filter]");
  if (search) {
    var typed = function () { return search.value.trim(); };
    document.addEventListener("submit", function (e) {
      e.target.querySelectorAll("input[data-keep-q]").forEach(function (f) { f.value = typed(); });
    });
    document.querySelectorAll("a[data-keep-q]").forEach(function (a) {
      a.addEventListener("click", function () {
        var base = a.getAttribute("href").split("?")[0];
        a.setAttribute("href", typed() === "" ? base : base + "?q=" + encodeURIComponent(typed()));
      });
    });
  }

  // Escape closes an open popover form and returns focus to its button.
  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") { return; }
    document.querySelectorAll("details.menu[open]").forEach(function (d) {
      d.open = false;
      var s = d.querySelector("summary");
      if (s) { s.focus(); }
    });
  });
});
