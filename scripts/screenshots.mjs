// screenshots.mjs - drive headless Chromium over the DevTools protocol to
// capture the management UI (and a couple of pages seen through the proxy).
//
// Called by scripts/capture_screenshots.sh, which builds the binary, seeds
// sample data and starts the server first. Needs only Node 22+ (global
// WebSocket) and a Chromium/Chrome binary - no npm packages.
//
//   node scripts/screenshots.mjs --base http://127.0.0.1:8099 \
//        --proxy 127.0.0.1:8080 --chrome /usr/bin/chromium --out screenshots
//
// Unlike `chromium --screenshot`, this can run page JavaScript before the
// capture: expand a collapsed policy-editor section and clip to it, switch
// the UI to dark mode, or browse through the proxy.
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync, mkdirSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";

const args = Object.fromEntries(
  process.argv.slice(2).reduce((acc, a, i, all) => {
    if (a.startsWith("--")) acc.push([a.slice(2), all[i + 1]]);
    return acc;
  }, []),
);
const BASE = args.base || "http://127.0.0.1:8099";
const PROXY = args.proxy || "127.0.0.1:8080";
const CHROME = args.chrome || "chromium";
const OUT = args.out || "screenshots";
const ONLY = args.only ? new Set(args.only.split(",")) : null;
mkdirSync(OUT, { recursive: true });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---------------------------------------------------------------------------
// A tiny origin for the through-the-proxy shots: an article with profanity
// (English plus a German quote) and ad slots the stand-in lists match.
// ---------------------------------------------------------------------------
const ARTICLE = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Weekend repair log</title>
<style>
 body{font:16px/1.6 system-ui,sans-serif;max-width:720px;margin:32px auto;padding:0 16px;color:#1f2937}
 h1{font-size:26px;margin:0 0 4px} .meta{color:#6b7280;font-size:13px;margin-bottom:20px}
 .ad-banner,.ad-slot,#sponsored{background:#fde68a;border:2px dashed #d97706;color:#92400e;
   padding:18px;margin:18px 0;text-align:center;font-weight:600}
 blockquote{border-left:4px solid #93c5fd;margin:16px 0;padding:4px 14px;color:#374151}
</style></head><body>
<div class="ad-banner">ADVERTISEMENT - 70% OFF EVERYTHING, TODAY ONLY</div>
<h1>Weekend repair log</h1><div class="meta">Posted by a frustrated tinkerer</div>
<p>The printer jammed again on Saturday, and honestly, this shit has to stop. I opened the
tray, pulled out a crumpled page and swore at the damn thing like a lunatic.</p>
<div id="sponsored">Sponsored: Printer ink subscriptions from $4.99/month</div>
<p>My neighbour, who grew up in Hamburg, wandered over and offered some advice:</p>
<blockquote lang="de">"Du Arschloch, du musst die Walze reinigen!"</blockquote>
<p>He was right. Ten minutes with a cloth and the bastard prints perfectly again.</p>
<div class="ad-slot">AD SLOT 300x250</div>
<p>Moral of the story: clean your rollers.</p>
</body></html>`;

function startOrigin() {
  return new Promise((resolve) => {
    const srv = createServer((req, res) => {
      res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
      res.end(ARTICLE);
    });
    srv.listen(0, "127.0.0.1", () => resolve(srv));
  });
}

// ---------------------------------------------------------------------------
// Minimal CDP client.
// ---------------------------------------------------------------------------
async function launch(extraArgs = []) {
  // Chromium puts a unix socket under TMPDIR; socket paths are limited to
  // ~108 bytes, so a deep TMPDIR makes it refuse to start.
  const base = process.platform !== "win32" && tmpdir().length > 60 ? "/tmp" : tmpdir();
  const profile = mkdtempSync(join(base, "wf-shots-"));
  const proc = spawn(CHROME, [
    "--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
    "--hide-scrollbars", "--no-first-run", "--no-default-browser-check",
    "--remote-debugging-port=0", `--user-data-dir=${profile}`, ...extraArgs, "about:blank",
  ], { stdio: ["ignore", "ignore", "pipe"], env: { ...process.env, TMPDIR: base } });
  const wsURL = await new Promise((resolve, reject) => {
    let buf = "";
    const timer = setTimeout(() => reject(new Error("chromium did not start:\n" + buf)), 20000);
    proc.stderr.on("data", (d) => {
      buf += d;
      const m = buf.match(/DevTools listening on (ws:\/\/\S+)/);
      if (m) { clearTimeout(timer); resolve(m[1]); }
    });
  });
  const ws = new WebSocket(wsURL);
  await new Promise((r) => ws.addEventListener("open", r, { once: true }));
  let id = 0;
  const pending = new Map();
  const listeners = [];
  ws.addEventListener("message", (ev) => {
    const msg = JSON.parse(ev.data);
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      msg.error ? reject(new Error(JSON.stringify(msg.error))) : resolve(msg.result);
    } else if (msg.method) {
      for (const l of listeners) l(msg);
    }
  });
  const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
    const mid = ++id;
    pending.set(mid, { resolve, reject });
    ws.send(JSON.stringify({ id: mid, method, params, sessionId }));
  });
  const waitFor = (method, sessionId, ms = 15000) => new Promise((resolve) => {
    const t = setTimeout(() => resolve(null), ms);
    const l = (msg) => {
      if (msg.method === method && msg.sessionId === sessionId) {
        clearTimeout(t);
        listeners.splice(listeners.indexOf(l), 1);
        resolve(msg);
      }
    };
    listeners.push(l);
  });
  const close = () => {
    try { ws.close(); } catch {}
    proc.kill();
    try { rmSync(profile, { recursive: true, force: true }); } catch {}
  };
  return { send, waitFor, close };
}

async function shoot(browser, shot) {
  const { send, waitFor } = browser;
  const { targetId } = await send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
  const s = (m, p) => send(m, p, sessionId);
  await s("Page.enable");
  await s("Runtime.enable");
  await s("Emulation.setDeviceMetricsOverride", {
    width: shot.width, height: shot.height, deviceScaleFactor: 1, mobile: false,
  });
  // chrome.js reads the theme from localStorage before first paint.
  await s("Page.addScriptToEvaluateOnNewDocument", {
    source: `try{localStorage.setItem('wf_theme', ${JSON.stringify(shot.theme || "light")})}catch(e){}`,
  });
  if (shot.warm) {
    // The first pass through the proxy compiles the adblock lists in the
    // background; load once to warm them, then load again for the capture.
    const warm = waitFor("Page.loadEventFired", sessionId);
    await s("Page.navigate", { url: shot.url });
    await warm;
    await sleep(2000);
  }
  const loaded = waitFor("Page.loadEventFired", sessionId);
  await s("Page.navigate", { url: shot.url });
  await loaded;
  await sleep(shot.settle ?? 1500); // Alpine fetches its data after load
  if (shot.prepare) {
    await s("Runtime.evaluate", { expression: `(async () => { ${shot.prepare} })()`, awaitPromise: true });
    await sleep(600);
  }
  let clip;
  if (shot.clip) {
    const { result } = await s("Runtime.evaluate", {
      expression: `(() => { const el = ${shot.clip}; if (!el) return null;
        const r = el.getBoundingClientRect();
        return JSON.stringify({x: r.left + scrollX, y: r.top + scrollY, width: r.width, height: r.height}); })()`,
      returnByValue: true,
    });
    if (result.value) {
      const r = JSON.parse(result.value);
      const pad = 16;
      clip = { x: Math.max(0, r.x - pad), y: Math.max(0, r.y - pad), width: r.width + 2 * pad, height: r.height + 2 * pad, scale: 1 };
    }
  }
  const { data } = await s("Page.captureScreenshot", { format: "png", captureBeyondViewport: !!clip, ...(clip ? { clip } : {}) });
  writeFileSync(join(OUT, shot.name + ".png"), Buffer.from(data, "base64"));
  console.log(`[shots]   ${shot.name}.png`);
  await send("Target.closeTarget", { targetId });
}

// Expand one policy-editor section and return the element to clip to.
const editorSection = (key, title) => ({
  prepare: `const d = Alpine.$data(document.body); d.open.${key} = true; await new Promise(r => setTimeout(r, 300));`,
  clip: `[...document.querySelectorAll('.section')].find(s => s.querySelector('.section-header') && s.querySelector('.section-header').textContent.includes(${JSON.stringify(title)}))`,
});

const origin = await startOrigin();
const ORIGIN = `http://127.0.0.1:${origin.address().port}`;

const uiShots = [
  { name: "dashboard", url: `${BASE}/index.html`, width: 1440, height: 1000 },
  { name: "dashboard-dark", url: `${BASE}/index.html`, width: 1440, height: 1000, theme: "dark" },
  { name: "policies", url: `${BASE}/policies.html`, width: 1440, height: 680 },
  { name: "policy-editor", url: `${BASE}/policy-editor.html?name=kids`, width: 1440, height: 1400 },
  { name: "editor-url-filter", url: `${BASE}/policy-editor.html?name=kids`, width: 1440, height: 1600, ...editorSection("url", "URL Filter") },
  { name: "editor-adblock", url: `${BASE}/policy-editor.html?name=kids`, width: 1440, height: 1600, ...editorSection("adblock", "Ad & Tracker") },
  { name: "editor-text-classifier", url: `${BASE}/policy-editor.html?name=kids`, width: 1440, height: 1800, ...editorSection("text", "Text Classifier") },
  { name: "editor-safesearch", url: `${BASE}/policy-editor.html?name=kids`, width: 1440, height: 1800, ...editorSection("safe", "SafeSearch") },
  { name: "logs", url: `${BASE}/logs.html`, width: 1440, height: 1100 },
  { name: "logs-requests", url: `${BASE}/logs.html`, width: 1440, height: 1100,
    prepare: `const d = Alpine.$data(document.body); d.tab = 'requests'; await d.load(); await new Promise(r => setTimeout(r, 500));` },
  { name: "logs-policy-changes", url: `${BASE}/logs.html`, width: 1440, height: 800,
    prepare: `const d = Alpine.$data(document.body); d.tab = 'policy_changes'; await d.load(); await new Promise(r => setTimeout(r, 500));` },
  { name: "analytics", url: `${BASE}/analytics.html`, width: 1440, height: 1250 },
  { name: "tools", url: `${BASE}/tools.html`, width: 1440, height: 1500 },
  { name: "settings", url: `${BASE}/settings.html`, width: 1440, height: 1400 },
  { name: "settings-adblock", url: `${BASE}/settings.html`, width: 1440, height: 4200,
    clip: `[...document.querySelectorAll('div.bg-card')].find(d => d.textContent.includes('Ad & Tracker Filter Lists'))` },
  { name: "login", url: `${BASE}/login.html`, width: 1100, height: 700 },
];

// Through the proxy: Chromium bypasses proxies for loopback unless told not to.
const proxyShots = [
  { name: "block-page", url: "http://blocked.example/", width: 1100, height: 760 },
  { name: "censor-after", url: `${ORIGIN}/article.html`, width: 900, height: 860, warm: true },
];
const directShots = [
  { name: "censor-before", url: `${ORIGIN}/article.html`, width: 900, height: 860 },
];

const pick = (list) => list.filter((s) => !ONLY || ONLY.has(s.name));
try {
  for (const [list, extra] of [
    [uiShots, []],
    [proxyShots, [`--proxy-server=http://${PROXY}`, "--proxy-bypass-list=<-loopback>"]],
    [directShots, []],
  ]) {
    const todo = pick(list);
    if (todo.length === 0) continue;
    const browser = await launch(extra);
    try {
      for (const shot of todo) await shoot(browser, shot);
    } finally {
      browser.close();
    }
  }
} finally {
  origin.close();
}
