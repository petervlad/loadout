// Draws one diagram inside the sandboxed frame of a preview page. The
// frame has an opaque origin, no network and no popups (its CSP and
// sandbox), so a bug in a diagram library reaches nothing of the page.
// The source comes from the package file on the server, in #src, never
// from the URL. When drawn, the frame tells the page its height.
//
// This script runs before the library (frame.html loads it first), so the
// draw.io viewer finds its settings, and draws once the library is in.
"use strict";

(function () {
  // The draw.io viewer fetches stencils, images, math and translations
  // from diagrams.net unless told otherwise. Point everything at a path
  // here that does not exist, and turn resource loading off; the frame's
  // CSP would block any of it anyway.
  var nowhere = window.location.origin + "/auth/static/vendor/none";
  ["PROXY_URL", "STYLE_PATH", "SHAPES_PATH", "STENCIL_PATH", "DRAW_MATH_URL", "GRAPH_IMAGE_PATH",
    "mxImageBasePath", "mxBasePath", "DRAWIO_BASE_URL", "DRAWIO_SERVER_URL", "RESOURCES_PATH"].forEach(function (name) {
    window[name] = nowhere;
  });
  window.mxLoadResources = false;
  window.mxLoadStylesheets = false;
  window.mxLanguage = "en";
  window.urlParams = { lang: "en", offline: "1" };

  var out, err;

  function dark() {
    var theme = document.documentElement.getAttribute("data-theme");
    if (theme) { return theme === "dark"; }
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  }

  // The page is on the frame's URL origin; only it may hear the height.
  function tellHeight() {
    // The body's own height: the document's would never shrink below
    // the frame. The page caps frames at 3000 px; only then does the
    // frame scroll itself.
    // The viewer lets its drawing overflow its container, so the SVG's
    // bottom counts too.
    var bottom = document.body.getBoundingClientRect().bottom;
    out.querySelectorAll("svg").forEach(function (svg) {
      bottom = Math.max(bottom, svg.getBoundingClientRect().bottom + 12);
    });
    var h = Math.ceil(bottom + window.scrollY) + 1;
    document.documentElement.style.overflowY = h > 3000 ? "auto" : "hidden";
    window.parent.postMessage({ loadout: "height", h: h }, window.location.origin);
  }

  function fail(e) {
    err.textContent = "This diagram could not be drawn: " + (e && e.message ? e.message : String(e));
    err.hidden = false;
    tellHeight();
  }

  function drawMermaid(src) {
    var m = window.mermaid;
    m.initialize({
      startOnLoad: false,
      // Text is encoded and click handlers are off.
      securityLevel: "strict",
      // Labels are SVG text, never HTML in a foreignObject.
      htmlLabels: false,
      flowchart: { htmlLabels: false, useMaxWidth: true },
      maxTextSize: 50000,
      maxEdges: 500,
      suppressErrorRendering: true,
      theme: dark() ? "dark" : "default",
      // An %%{init}%% directive in a diagram may not change these.
      secure: ["secure", "securityLevel", "startOnLoad", "maxTextSize", "maxEdges", "suppressErrorRendering",
        "htmlLabels", "flowchart", "dompurifyConfig", "themeCSS"]
    });
    m.render("diagram", src).then(function (res) {
      out.innerHTML = res.svg;
      tellHeight();
    }, fail);
  }

  function drawDrawio(src) {
    var doc = window.mxUtils.parseXml(src);
    // Built here, never read from the page: the viewer's own data-mxgraph
    // attribute would be JSON the server writes into HTML.
    var viewer = new window.GraphViewer(out, doc.documentElement, {
      page: Number(document.body.getAttribute("data-page")) || 0,
      toolbar: "pages zoom layers",
      "toolbar-nohide": true,
      nav: true,
      resize: true,
      lightbox: false,
      edit: null,
      highlight: "#0066cc",
      // Fit the frame's width; the zoom buttons enlarge it.
      "auto-fit": true,
      // The author's colors, on a light canvas in both themes.
      "dark-mode": "light"
    });
    viewer.addListener("render", tellHeight);
    tellHeight();
  }

  document.addEventListener("DOMContentLoaded", function () {
    out = document.getElementById("out");
    err = document.getElementById("err");
    var src = document.getElementById("src").textContent;
    var draw = { mermaid: drawMermaid, drawio: drawDrawio }[document.body.getAttribute("data-kind")];
    try {
      if (!draw) { throw new Error("this kind of diagram is not known here"); }
      draw(src);
    } catch (e) {
      fail(e);
    }
    window.addEventListener("resize", tellHeight);
    // Late layout (fonts, the viewer's toolbar) changes the height too.
    if (window.ResizeObserver) { new ResizeObserver(tellHeight).observe(document.body); }
  });
})();
