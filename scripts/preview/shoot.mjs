// Screenshots through the Chrome DevTools Protocol: exact viewport width
// (headless --window-size has a minimum), cookies, color scheme, full page.
// It also prints what the page and every frame in it logged: console
// errors and warnings, uncaught exceptions and Content-Security-Policy
// violations, so a diagram frame that the CSP breaks shows up here.
// Usage: node shoot.mjs jobs.json
//
// Pages are opened on the registry's public URL, http://localhost with
// LOADOUT_PREVIEW_PORT (7431): the diagram frames' CSP names that origin,
// so 127.0.0.1 would not load their scripts. LOADOUT_CDP_PORT (9333) is
// Chrome's debugging port.
import { spawn } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";

const CH = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const PORT = Number(process.env.LOADOUT_CDP_PORT || 9333);
const BASE = "http://localhost:" + (process.env.LOADOUT_PREVIEW_PORT || 7431);
const jobs = JSON.parse(readFileSync(process.argv[2], "utf8"));
const chrome = spawn(CH, ["--headless", "--disable-gpu", `--remote-debugging-port=${PORT}`,
  "--user-data-dir=" + (process.env.LOADOUT_PREVIEW_DIR || (process.env.TMPDIR || "/tmp") + "/loadout-preview") + "/cdp-profile", "about:blank"], { stdio: "ignore" });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let target;
for (let i = 0; i < 100 && !target; i++) {
  try {
    const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
    target = list.find((t) => t.type === "page");
  } catch { await sleep(100); }
}
const ws = new WebSocket(target.webSocketDebuggerUrl);
await new Promise((r) => ws.addEventListener("open", r));
let seq = 0;
const pending = new Map();
const waiters = [];
let logged = [];
const send = (method, params = {}, sessionId) => new Promise((r) => {
  const id = ++seq;
  pending.set(id, r);
  ws.send(JSON.stringify(sessionId ? { id, method, params, sessionId } : { id, method, params }));
});
// Every frame, the out-of-process ones (sandboxed frames) included, logs
// into the same list.
const listen = async (sessionId) => {
  await send("Log.enable", {}, sessionId);
  await send("Runtime.enable", {}, sessionId);
  await send("Target.setAutoAttach", { autoAttach: true, waitForDebuggerOnStart: true, flatten: true }, sessionId);
  if (sessionId) { await send("Runtime.runIfWaitingForDebugger", {}, sessionId); }
};
ws.addEventListener("message", (e) => {
  const m = JSON.parse(e.data);
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); }
  if (!m.method) { return; }
  const where = m.sessionId ? "frame" : "page";
  if (m.method === "Target.attachedToTarget") { listen(m.params.sessionId); }
  if (m.method === "Log.entryAdded" && m.params.entry.level !== "verbose") {
    logged.push(`${where} ${m.params.entry.level} [${m.params.entry.source}] ${m.params.entry.text}${m.params.entry.url ? " " + m.params.entry.url : ""}`);
  }
  if (m.method === "Runtime.consoleAPICalled" && ["error", "warning", "assert"].includes(m.params.type)) {
    logged.push(`${where} console.${m.params.type} ${m.params.args.map((a) => a.value ?? a.description ?? "").join(" ")}`);
  }
  if (m.method === "Runtime.exceptionThrown") {
    const d = m.params.exceptionDetails;
    logged.push(`${where} exception ${d.exception?.description || d.text}`);
  }
  if (!m.sessionId) { for (const w of waiters.splice(0)) { w(m); } }
});
const loaded = () => new Promise((r) => { const check = (m) => (m.method === "Page.loadEventFired" ? r() : waiters.push(check)); waiters.push(check); });

await send("Page.enable");
await send("Network.enable");
await listen();
const report = [];
for (const j of jobs) {
  logged = [];
  await send("Network.clearBrowserCookies");
  for (const [name, value] of Object.entries(j.cookies || {})) {
    await send("Network.setCookie", { name, value, url: BASE + "/" });
  }
  await send("Emulation.setEmulatedMedia", { features: [
    { name: "prefers-color-scheme", value: j.scheme || "light" },
    { name: "prefers-contrast", value: j.contrast || "no-preference" },
  ] });
  await send("Emulation.setDeviceMetricsOverride", { width: j.width || 1280, height: j.height || 820, deviceScaleFactor: 1, mobile: (j.width || 1280) < 500 });
  // A touch screen makes (pointer: coarse) match, for the 44 px targets.
  await send("Emulation.setTouchEmulationEnabled", { enabled: !!j.touch, maxTouchPoints: j.touch ? 5 : 0 });
  await send("Emulation.setScriptExecutionDisabled", { value: !!j.nojs });
  const done = loaded();
  await send("Page.navigate", { url: BASE + j.path });
  await done;
  // Diagram frames draw after the load event; give them time.
  await sleep(j.wait || 150);
  if (j.js) { await send("Runtime.evaluate", { expression: j.js }); await sleep(j.jsWait || 300); }
  const shot = await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: !!j.full });
  writeFileSync(`${j.out}`, Buffer.from(shot.result.data, "base64"));
  console.log("saved", j.out);
  for (const line of logged) { console.log("  " + line); }
  report.push({ out: j.out, path: j.path, logged });
}
if (process.env.LOADOUT_SHOOT_LOG) { writeFileSync(process.env.LOADOUT_SHOOT_LOG, JSON.stringify(report, null, 1)); }
ws.close();
chrome.kill();
