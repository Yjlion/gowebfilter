// WebFilter background (Firefox MV3 event page). Owns:
//  - settings (browser.storage.local, merged over DEFAULT_SETTINGS)
//  - compiling settings into declarativeNetRequest dynamic rules
//    (safesearch, tab blocks, URL allow/block lists) and toggling the
//    static DoH ruleset
//  - the weekly schedule (ported from internal/models/schedule.go —
//    fail-open: disabled or empty-window schedules mean "always active")
//  - scoring services for the content scripts (adult text via bayes.js,
//    NSFW images via nsfw.js)
//
// Loaded after vendor/tf.min.js, rules_data.js, bayes_model.js, bayes.js,
// nsfw.js (classic scripts, shared globals — see manifest.json).

"use strict";

const BLOCKED_PAGE = "/pages/blocked.html";

const bayesModel = new BayesModel(BAYES_MODEL);

// --- settings --------------------------------------------------------------

function deepMerge(defaults, stored) {
  if (stored === undefined || stored === null) return structuredClone(defaults);
  if (Array.isArray(defaults) || typeof defaults !== "object") return stored;
  const out = {};
  for (const k of Object.keys(defaults)) {
    out[k] = deepMerge(defaults[k], stored[k]);
  }
  return out;
}

async function loadSettings() {
  const { settings } = await browser.storage.local.get("settings");
  return deepMerge(DEFAULT_SETTINGS, settings);
}

// --- schedule (models/schedule.go IsActiveAt) -------------------------------

function parseHHMM(v) {
  const m = /^(\d{1,2}):(\d{1,2})$/.exec(v || "");
  if (!m) return null;
  const h = Number(m[1]);
  const min = Number(m[2]);
  if (h < 0 || h > 23 || min < 0 || min > 59) return null;
  return h * 60 + min;
}

function scheduleIsActive(schedule, now = new Date()) {
  if (!schedule.enabled || !schedule.windows || schedule.windows.length === 0) {
    return true; // fail open
  }
  // 0=Monday..6=Sunday (ISO weekday - 1), matching the Go/Python models.
  const weekday = (now.getDay() + 6) % 7;
  const hm = now.getHours() * 60 + now.getMinutes();

  for (const w of schedule.windows) {
    const days = w.days && w.days.length ? w.days.map((d) => ((d % 7) + 7) % 7) : [0, 1, 2, 3, 4, 5, 6];
    const startMin = parseHHMM(w.start) ?? 0;
    const endMin = parseHHMM(w.end) ?? 23 * 60 + 59;
    if (startMin <= endMin) {
      if (days.includes(weekday) && hm >= startMin && hm <= endMin) return true;
      continue;
    }
    // Overnight window: Monday 22:00-06:00 is active late Monday and early
    // Tuesday.
    if (days.includes(weekday) && hm >= startMin) return true;
    const prevWeekday = (weekday + 6) % 7;
    if (days.includes(prevWeekday) && hm <= endMin) return true;
  }
  return false;
}

function filterActive(settings) {
  return settings.enabled && scheduleIsActive(settings.schedule);
}

// --- URL allow/block helpers ------------------------------------------------

// hostFromEntry normalizes a user list entry ("example.com",
// "https://example.com/x") to a bare hostname.
function hostFromEntry(entry) {
  let s = String(entry || "").trim().toLowerCase();
  if (s === "") return "";
  s = s.replace(/^[a-z][a-z0-9+.-]*:\/\//, "");
  s = s.split(/[/?#]/)[0];
  s = s.replace(/:\d+$/, "");
  return s;
}

// hostInList mirrors the Go side's domain matching: exact host or any
// subdomain of a listed domain.
function hostInList(host, list) {
  for (const entry of list) {
    const d = hostFromEntry(entry);
    if (d && (host === d || host.endsWith("." + d))) return true;
  }
  return false;
}

function urlIsAllowed(url, settings) {
  if (!settings.urlFilter.enabled) return false;
  let host;
  try {
    host = new URL(url).hostname;
  } catch {
    return false;
  }
  return hostInList(host, settings.urlFilter.allow);
}

// --- DNR rule compilation ----------------------------------------------------

const HTTP_TYPES_NO_MAIN = ["sub_frame", "xmlhttprequest", "media", "websocket", "other"];

function blockedRedirect(reason) {
  return {
    type: "redirect",
    redirect: { extensionPath: `${BLOCKED_PAGE}?reason=${encodeURIComponent(reason)}` },
  };
}

// blockPair emits a main_frame redirect to the block page plus a plain
// block for subresources — mirroring the proxy, where blocked navigations
// render a block page while blocked assets just fail.
function blockPair(rules, condition, reason, priority) {
  rules.push({
    priority,
    action: blockedRedirect(reason),
    condition: { ...condition, resourceTypes: ["main_frame"] },
  });
  rules.push({
    priority,
    action: { type: "block" },
    condition: { ...condition, resourceTypes: HTTP_TYPES_NO_MAIN },
  });
}

function safesearchRules(rules, cfg) {
  if (!cfg.enabled) return;

  for (const [name, engine] of Object.entries(SEARCH_ENGINES)) {
    const engCfg = cfg.engines[name] || { enabled: true };
    if (!engCfg.enabled) continue;

    // Param enforcement: redirect with a query transform. A transform that
    // produces the same URL is ignored by DNR, so this cannot loop.
    if (engine.safeParam) {
      rules.push({
        priority: 5,
        action: {
          type: "redirect",
          redirect: {
            transform: {
              queryTransform: {
                addOrReplaceParams: [{ key: engine.safeParam.key, value: engine.safeParam.value }],
              },
            },
          },
        },
        condition: {
          regexFilter: `^https?://${engine.paramHostRe}${engine.paramPathRe}`,
          resourceTypes: ["main_frame", "sub_frame", "xmlhttprequest"],
        },
      });
    }

    // Header enforcement (YouTube Restricted Mode) — all paths.
    if (engine.safeHeader) {
      rules.push({
        priority: 5,
        action: {
          type: "modifyHeaders",
          requestHeaders: [
            { header: engine.safeHeader.name, operation: "set", value: engine.safeHeader.value },
          ],
        },
        condition: {
          requestDomains: engine.headerDomains,
          resourceTypes: ["main_frame", "sub_frame", "xmlhttprequest", "media", "other"],
        },
      });
    }

    const tabs = [
      ["imagesTab", engCfg.blockImagesTab, "Image search blocked by policy"],
      ["videosTab", engCfg.blockVideosTab, "Video search blocked by policy"],
      ["aiTab", engCfg.blockAiTab, "AI search blocked by policy"],
    ];
    for (const [key, on, reason] of tabs) {
      const tab = engine[key];
      if (!on || !tab) continue;
      for (const re of [...(tab.pathRes || []), ...(tab.paramRes || [])]) {
        blockPair(rules, { regexFilter: `^https?://${re}` }, reason, 20);
      }
      if (tab.domains) {
        blockPair(rules, { requestDomains: tab.domains }, reason, 20);
      }
      // Image CDN hosts serve image results wholesale for this engine —
      // block outright (plain block: they're subresources).
      for (const re of tab.cdnRes || []) {
        rules.push({
          priority: 20,
          action: { type: "block" },
          condition: { regexFilter: `^https?://${re}/` },
        });
      }
      if (tab.cdnDomains) {
        rules.push({
          priority: 20,
          action: { type: "block" },
          condition: { requestDomains: tab.cdnDomains },
        });
      }
    }
  }
}

function urlFilterRules(rules, cfg) {
  if (!cfg.enabled) return;

  const allowDomains = cfg.allow.map(hostFromEntry).filter(Boolean);
  const blockDomains = cfg.block.map(hostFromEntry).filter(Boolean);

  // Allow rules outrank every block/redirect below (priority 100) —
  // mirroring the proxy, where an allow-list hit short-circuits filtering.
  if (allowDomains.length) {
    rules.push({
      priority: 100,
      action: { type: "allow" },
      condition: { requestDomains: allowDomains },
    });
  }

  if (blockDomains.length) {
    blockPair(rules, { requestDomains: blockDomains }, "url_filter", 10);
  }

  // Whitelist mode: block everything that isn't explicitly allowed.
  if (cfg.mode === "whitelist") {
    blockPair(rules, { regexFilter: "^https?://" }, "url_filter", 1);
  }
}

// compileRules is the single settings→dynamic-rules composition, exercised
// directly by test/rules_check.mjs — keep applyRules a thin wrapper so the
// test can't drift from what the extension actually installs.
function compileRules(settings) {
  const rules = [];
  if (filterActive(settings)) {
    safesearchRules(rules, settings.safesearch);
    urlFilterRules(rules, settings.urlFilter);
  }
  rules.forEach((r, i) => {
    r.id = i + 1;
  });
  return rules;
}

async function applyRules(settings) {
  const active = filterActive(settings);
  const rules = compileRules(settings);

  const existing = await browser.declarativeNetRequest.getDynamicRules();
  await browser.declarativeNetRequest.updateDynamicRules({
    removeRuleIds: existing.map((r) => r.id),
    addRules: rules,
  });

  const dohOn = active && settings.doh.blockEndpoints;
  await browser.declarativeNetRequest.updateEnabledRulesets({
    [dohOn ? "enableRulesetIds" : "disableRulesetIds"]: ["doh"],
  });
}

async function refresh() {
  const settings = await loadSettings();
  try {
    await applyRules(settings);
  } catch (err) {
    console.error("webfilter: applying rules failed:", err);
  }

  // The schedule needs re-evaluation over time, not just on settings
  // changes. A one-minute alarm matches the schedule's HH:MM granularity.
  if (settings.enabled && settings.schedule.enabled && settings.schedule.windows.length) {
    browser.alarms.create("schedule-tick", { periodInMinutes: 1 });
  } else {
    browser.alarms.clear("schedule-tick");
  }
  return settings;
}

// --- content-script services -------------------------------------------------

const imageVerdictCache = new Map(); // url -> score (session-scoped)
const IMAGE_CACHE_MAX = 2000;

async function handleMessage(msg, sender) {
  const settings = await loadSettings();
  const active = filterActive(settings);
  const url = sender?.url || "";
  const allowed = urlIsAllowed(url, settings);

  switch (msg.type) {
    case "getState":
      return {
        active: active && !allowed,
        textClassifier: settings.textClassifier,
        imageClassifier: settings.imageClassifier,
        youtube: settings.youtube,
      };

    case "scoreText": {
      if (!active || allowed || !settings.textClassifier.enabled) {
        return { blocked: false };
      }
      const text = String(msg.text || "").slice(0, 500_000);
      return classifyText(bayesModel, text, settings.textClassifier.threshold);
    }

    case "classifyImage": {
      if (!active || allowed || !settings.imageClassifier.enabled) {
        return { ok: false, filtered: false };
      }
      const imgUrl = String(msg.url || "");
      if (!/^https?:|^data:image\//.test(imgUrl)) {
        return { ok: false, filtered: false };
      }
      let score = imageVerdictCache.get(imgUrl);
      if (score === undefined) {
        const res = await scoreImageUrl(imgUrl);
        if (!res.ok) return { ok: false, filtered: false };
        score = res.score;
        if (imageVerdictCache.size >= IMAGE_CACHE_MAX) {
          imageVerdictCache.delete(imageVerdictCache.keys().next().value);
        }
        imageVerdictCache.set(imgUrl, score);
      }
      return {
        ok: true,
        score,
        filtered: score >= settings.imageClassifier.threshold,
        action: settings.imageClassifier.action,
      };
    }

    default:
      return undefined;
  }
}

browser.runtime.onMessage.addListener((msg, sender) => handleMessage(msg, sender));

browser.storage.onChanged.addListener((changes, area) => {
  if (area === "local" && changes.settings) refresh();
});

browser.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === "schedule-tick") refresh();
});

browser.runtime.onInstalled.addListener(refresh);
browser.runtime.onStartup.addListener(refresh);
refresh();
