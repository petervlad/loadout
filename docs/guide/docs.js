// Opens the chapter a link points into. Chrome, Edge and Firefox do this
// themselves for a link into a closed <details>; Safari does not, and none
// of them open it when the link is to the chapter's own heading. Also adds
// "Open all" and "Close all", points "loadout docs" back to the registry
// page that opened the docs, and keeps that page on links to the other
// docs pages. The page reads fine without this script.
"use strict";
(function () {
  function openTarget() {
    var id = decodeURIComponent(location.hash.slice(1));
    var el = id && document.getElementById(id);
    if (!el) { return; }
    for (var d = el.closest("details"); d; d = d.parentElement && d.parentElement.closest("details")) {
      d.open = true;
    }
    el.scrollIntoView();
    var menu = document.querySelector(".mobile-toc details");
    if (menu) { menu.open = false; }
  }
  window.addEventListener("hashchange", openTarget);
  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest('a[href^="#"]');
    // A click on the link already in the address bar fires no hashchange.
    if (a && a.getAttribute("href") === location.hash) { setTimeout(openTarget, 0); }
  });
  // The registry page to go back to is kept for the tab, so a docs page
  // opened without ?from (a link that lost it, an address typed again)
  // still leads back. sessionStorage can be off or full; then only ?from
  // counts.
  var backKey = "loadout-docs-back";
  // loadoutPage is from when it is a page on a loadout site (or a local
  // one while developing). Any other address is ignored, so a crafted link
  // cannot turn "loadout docs" into a link to somewhere else.
  function loadoutPage(from) {
    if (!from) { return null; }
    try {
      var u = new URL(from);
      var host = u.hostname;
      var loadout = u.protocol === "https:" && (host === "loadout.lat" || host.slice(-12) === ".loadout.lat");
      var local = u.protocol === "http:" && (host === "localhost" || host === "127.0.0.1");
      return loadout || local ? u.href : null;
    } catch (e) {
      return null;
    }
  }
  // backURL is the registry page in ?from, which replaces the one kept for
  // the tab, or else the kept one.
  function backURL() {
    var back = loadoutPage(new URLSearchParams(location.search).get("from"));
    try {
      if (back) {
        sessionStorage.setItem(backKey, back);
      } else {
        back = loadoutPage(sessionStorage.getItem(backKey));
      }
    } catch (e) {
      // No storage: the page in ?from, if any, is all there is.
    }
    return back;
  }
  function linkBack() {
    var back = backURL();
    if (!back) { return; }
    document.querySelectorAll("a.brand").forEach(function (a) {
      a.href = back;
      a.title = "Back to " + new URL(back).host;
    });
    // Links to the other docs pages (the Guide and What's new switch, links
    // in the text) keep ?from, so every page still leads back, in this tab
    // or a new one. Links within the page need nothing.
    document.querySelectorAll("a[href]").forEach(function (a) {
      var href = a.getAttribute("href");
      if (href.charAt(0) === "#" || a.classList.contains("brand")) { return; }
      var u = new URL(href, location.href);
      if (u.origin !== location.origin || u.pathname.indexOf("/docs/") !== 0) { return; }
      u.searchParams.set("from", back);
      a.href = u.pathname + u.search + u.hash;
    });
  }
  function all(open) {
    document.querySelectorAll("details.chapter").forEach(function (d) { d.open = open; });
  }
  document.addEventListener("DOMContentLoaded", function () {
    linkBack();
    var bar = document.getElementById("chapter-tools");
    if (bar) {
      bar.hidden = false;
      bar.querySelector("[data-open]").addEventListener("click", function () { all(true); });
      bar.querySelector("[data-close]").addEventListener("click", function () { all(false); });
    }
    openTarget();
  });
})();
