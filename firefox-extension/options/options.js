"use strict";

// Options page: reads/writes the storage.local "settings" object (the
// extension's analogue of a policies/*.json file). background.js recompiles
// declarativeNetRequest rules on every save via storage.onChanged.

const ENGINES = ["google", "bing", "duckduckgo", "yahoo", "youtube"];
const DAY_NAMES = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"]; // 0=Monday, matching the Go/Python models

const $ = (id) => document.getElementById(id);

// --- schedule text format ----------------------------------------------------

function parseDays(spec) {
  const days = new Set();
  for (const part of spec.toLowerCase().split(",")) {
    const range = part.split("-").map((d) => DAY_NAMES.indexOf(d.trim().slice(0, 3)));
    if (range.some((d) => d < 0)) return null;
    if (range.length === 1) {
      days.add(range[0]);
    } else {
      for (let d = range[0]; ; d = (d + 1) % 7) {
        days.add(d);
        if (d === range[1]) break;
      }
    }
  }
  return [...days].sort((a, b) => a - b);
}

function parseWindows(text) {
  const windows = [];
  for (const line of text.split("\n")) {
    const t = line.trim();
    if (!t) continue;
    const m = /^(.+?)\s+(\d{1,2}:\d{2})-(\d{1,2}:\d{2})$/.exec(t);
    if (!m) continue;
    const days = parseDays(m[1]);
    if (!days) continue;
    windows.push({ days, start: m[2], end: m[3] });
  }
  return windows;
}

function formatWindows(windows) {
  const cap = (s) => s[0].toUpperCase() + s.slice(1);
  return (windows || [])
    .map((w) => {
      const days = (w.days || []).map((d) => cap(DAY_NAMES[((d % 7) + 7) % 7])).join(",");
      return `${days} ${w.start}-${w.end}`;
    })
    .join("\n");
}

// --- load / save --------------------------------------------------------------

function linesToList(text) {
  return text
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);
}

function buildEngineRows(engines) {
  const table = $("ss-engines");
  for (const name of ENGINES) {
    const cfg = engines[name] || {};
    const tr = document.createElement("tr");
    const label = document.createElement("td");
    label.textContent = name;
    tr.appendChild(label);
    for (const key of ["enabled", "blockImagesTab", "blockVideosTab", "blockAiTab"]) {
      const td = document.createElement("td");
      // YouTube SafeSearch is header-based Restricted Mode only — no tabs.
      if (name === "youtube" && key !== "enabled") {
        tr.appendChild(td);
        continue;
      }
      const cb = document.createElement("input");
      cb.type = "checkbox";
      cb.dataset.engine = name;
      cb.dataset.key = key;
      cb.checked = key === "enabled" ? cfg.enabled !== false : !!cfg[key];
      td.appendChild(cb);
      tr.appendChild(td);
    }
    table.appendChild(tr);
  }
}

function collectEngines() {
  const engines = {};
  for (const cb of document.querySelectorAll("#ss-engines input[type=checkbox]")) {
    const { engine, key } = cb.dataset;
    engines[engine] = engines[engine] || {};
    engines[engine][key] = cb.checked;
  }
  return engines;
}

async function load() {
  const { settings: s = {} } = await browser.storage.local.get("settings");

  $("enabled").checked = s.enabled !== false;

  const ss = s.safesearch || {};
  $("ss-enabled").checked = !!ss.enabled;
  buildEngineRows(ss.engines || {});

  const uf = s.urlFilter || {};
  $("uf-enabled").checked = !!uf.enabled;
  document.querySelector(`input[name=uf-mode][value=${uf.mode === "whitelist" ? "whitelist" : "blacklist"}]`).checked = true;
  $("uf-block").value = (uf.block || []).join("\n");
  $("uf-allow").value = (uf.allow || []).join("\n");

  $("doh-block").checked = !!(s.doh || {}).blockEndpoints;

  const tc = s.textClassifier || {};
  $("tc-enabled").checked = !!tc.enabled;
  $("tc-threshold").value = tc.threshold ?? 0.8;

  const ic = s.imageClassifier || {};
  $("ic-enabled").checked = !!ic.enabled;
  $("ic-action").value = ic.action === "block" ? "block" : "blur";
  $("ic-threshold").value = ic.threshold ?? 0.75;
  $("ic-minsize").value = ic.minSize ?? 64;

  const yt = s.youtube || {};
  $("yt-enabled").checked = !!yt.enabled;
  document.querySelector(`input[name=yt-mode][value=${yt.mode === "whitelist" ? "whitelist" : "blacklist"}]`).checked = true;
  $("yt-channels").value = (yt.channels || []).join("\n");
  $("yt-home").checked = yt.blockHome !== false;
  $("yt-comments").checked = !!yt.removeComments;
  $("yt-recs").checked = !!yt.removeRecommendations;

  const sched = s.schedule || {};
  $("sched-enabled").checked = !!sched.enabled;
  $("sched-windows").value = formatWindows(sched.windows);
}

async function save() {
  const settings = {
    enabled: $("enabled").checked,
    safesearch: {
      enabled: $("ss-enabled").checked,
      engines: collectEngines(),
    },
    urlFilter: {
      enabled: $("uf-enabled").checked,
      mode: document.querySelector("input[name=uf-mode]:checked").value,
      block: linesToList($("uf-block").value),
      allow: linesToList($("uf-allow").value),
    },
    doh: { blockEndpoints: $("doh-block").checked },
    textClassifier: {
      enabled: $("tc-enabled").checked,
      threshold: Number($("tc-threshold").value) || 0.8,
    },
    imageClassifier: {
      enabled: $("ic-enabled").checked,
      action: $("ic-action").value,
      threshold: Number($("ic-threshold").value) || 0.75,
      minSize: Number($("ic-minsize").value) || 64,
    },
    youtube: {
      enabled: $("yt-enabled").checked,
      mode: document.querySelector("input[name=yt-mode]:checked").value,
      channels: linesToList($("yt-channels").value),
      blockHome: $("yt-home").checked,
      removeComments: $("yt-comments").checked,
      removeRecommendations: $("yt-recs").checked,
    },
    schedule: {
      enabled: $("sched-enabled").checked,
      windows: parseWindows($("sched-windows").value),
    },
  };

  await browser.storage.local.set({ settings });
  $("saved").textContent = "Saved.";
  setTimeout(() => {
    $("saved").textContent = "";
  }, 1500);
}

$("save").addEventListener("click", save);
load();
