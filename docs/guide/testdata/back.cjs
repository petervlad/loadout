// Runs docs.js in a bare page, once per case read as JSON from stdin, and
// prints what its links point at. Driven by TestBackLink.
"use strict";
const fs = require("fs");
const vm = require("vm");

const script = fs.readFileSync(process.argv[2], "utf8");
const cases = JSON.parse(fs.readFileSync(0, "utf8"));

function link(href, brand) {
  return {
    href: href,
    title: "",
    getAttribute: function (name) { return name === "href" ? href : null; },
    classList: { contains: function (c) { return brand && c === "brand"; } },
  };
}

function run(c) {
  const stored = new Map(c.stored ? [["loadout-docs-back", c.stored]] : []);
  const storage = c.noStorage ? {
    getItem: function () { throw new Error("denied"); },
    setItem: function () { throw new Error("denied"); },
  } : {
    getItem: function (k) { return stored.has(k) ? stored.get(k) : null; },
    setItem: function (k, v) { stored.set(k, String(v)); },
  };
  const brand = link("#top", true);
  const links = c.links.map(function (h) { return link(h, false); });
  const all = [brand].concat(links);
  let ready = null;
  const u = new URL(c.url);
  const document = {
    addEventListener: function (type, f) { if (type === "DOMContentLoaded") { ready = f; } },
    getElementById: function () { return null; },
    querySelector: function () { return null; },
    querySelectorAll: function (sel) {
      if (sel === "a.brand") { return [brand]; }
      if (sel === "a[href]") { return all; }
      return [];
    },
  };
  const context = {
    URL: URL, URLSearchParams: URLSearchParams, setTimeout: setTimeout,
    document: document,
    location: { href: u.href, search: u.search, hash: u.hash, origin: u.origin },
    sessionStorage: storage,
    window: { addEventListener: function () {} },
  };
  vm.runInNewContext(script, context);
  ready();
  return {
    brand: brand.href,
    links: links.map(function (l) { return l.href; }),
    stored: stored.get("loadout-docs-back") || "",
  };
}

process.stdout.write(JSON.stringify(cases.map(run)));
