"use strict";

// Quick feature toggles. The full configuration (engine tabs, lists,
// thresholds, schedule) lives in the options page; both read/write the same
// storage.local "settings" object that background.js compiles into rules.

const IDS = ["enabled", "safesearch", "urlFilter", "doh", "textClassifier", "imageClassifier", "youtube"];

async function getSettings() {
  const { settings } = await browser.storage.local.get("settings");
  return settings || {};
}

function flagValue(id, settings) {
  if (id === "enabled") return settings.enabled !== false;
  if (id === "doh") return !!settings.doh?.blockEndpoints;
  return !!settings[id]?.enabled;
}

function setFlag(id, settings, value) {
  if (id === "enabled") {
    settings.enabled = value;
  } else if (id === "doh") {
    settings.doh = { ...settings.doh, blockEndpoints: value };
  } else {
    settings[id] = { ...settings[id], enabled: value };
  }
}

async function render() {
  const settings = await getSettings();
  for (const id of IDS) {
    document.getElementById(id).checked = flagValue(id, settings);
  }
  const sched = settings.schedule || {};
  document.getElementById("status").textContent =
    sched.enabled && (sched.windows || []).length
      ? "Schedule active — filtering follows configured time windows."
      : "";
}

for (const id of IDS) {
  document.getElementById(id).addEventListener("change", async (e) => {
    const settings = await getSettings();
    setFlag(id, settings, e.target.checked);
    await browser.storage.local.set({ settings });
  });
}

document.getElementById("options").addEventListener("click", () => {
  browser.runtime.openOptionsPage();
  window.close();
});

render();
