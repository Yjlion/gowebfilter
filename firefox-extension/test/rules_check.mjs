// Structural test for the DNR rule compilation in background/background.js:
// compiles rules from a fully-enabled settings object and asserts the
// regexFilters hit and miss the right URLs (including the DuckDuckGo/Google
// same-domain tab gotchas ported from the Go safesearch tests).
//
//	node firefox-extension/test/rules_check.mjs

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const here = dirname(fileURLToPath(import.meta.url));
const bg = (f) => readFileSync(join(here, "..", "background", f), "utf8");

// Minimal browser stub so background.js's top-level listeners register.
const browserStub = {
  runtime: {
    onMessage: { addListener() {} },
    onInstalled: { addListener() {} },
    onStartup: { addListener() {} },
    getURL: (p) => `moz-extension://test/${p}`,
  },
  storage: {
    local: { get: async () => ({}) },
    onChanged: { addListener() {} },
  },
  alarms: { create() {}, clear() {}, onAlarm: { addListener() {} } },
  declarativeNetRequest: {
    getDynamicRules: async () => [],
    updateDynamicRules: async () => {},
    updateEnabledRulesets: async () => {},
  },
};

const sandbox = vm.createContext({
  console,
  browser: browserStub,
  structuredClone,
  setTimeout,
  URL,
});

// nsfw.js is deliberately not loaded: background.js only reaches it inside
// the classifyImage message handler, which this test never sends.
const src =
  `${bg("rules_data.js")}\n${bg("bayes_model.js")}\n${bg("bayes.js")}\n${bg("background.js")}\n` +
  `;({ compileRules, DEFAULT_SETTINGS, scheduleIsActive, hostInList });`;
const x = vm.runInContext(src, sandbox, { filename: "background-combined.js" });

let failures = 0;
function check(name, cond) {
  if (!cond) {
    failures++;
    console.error(`FAIL: ${name}`);
  }
}

// --- compile rules with everything enabled ---------------------------------

const settings = structuredClone(x.DEFAULT_SETTINGS);
settings.safesearch.enabled = true;
for (const e of Object.values(settings.safesearch.engines)) {
  e.blockImagesTab = true;
  e.blockVideosTab = true;
  e.blockAiTab = true;
}
settings.urlFilter.enabled = true;
settings.urlFilter.block = ["blockedsite.example"];
settings.urlFilter.allow = ["allowedsite.example"];

const rules = x.compileRules(settings);

check("compiles a meaningful rule set", rules.length > 15 && rules.length < 200);
check("all rules have valid ids", rules.every((r) => Number.isInteger(r.id) && r.id > 0));
check(
  "all regexFilters are valid RegExp",
  rules.every((r) => {
    if (!r.condition.regexFilter) return true;
    try {
      new RegExp(r.condition.regexFilter);
      return true;
    } catch {
      return false;
    }
  })
);
// RE2 (used by DNR) has no lookaround/backreferences — reject them early.
check(
  "no lookaround/backreferences in regexFilters",
  rules.every((r) => !/\(\?[=!<]|\\[1-9]/.test(r.condition.regexFilter || ""))
);

// --- regex behavior against sample URLs -------------------------------------

function matchers(pred) {
  return rules
    .filter((r) => r.condition.regexFilter && pred(r))
    .map((r) => new RegExp(r.condition.regexFilter));
}
// "Blocking" = a plain block or a redirect to the extension block page;
// query-transform redirects (safe-param injection) are not blocks.
const blockRegexes = matchers(
  (r) => r.action.type === "block" || r.action.redirect?.extensionPath
);
const anyBlockMatch = (url) => blockRegexes.some((re) => re.test(url));

const shouldBlock = [
  "https://www.google.com/search?q=cats&tbm=isch",
  "https://www.google.co.uk/search?q=cats&udm=2",
  "https://www.google.com/search?q=cats&udm=50",
  "https://www.google.com/imghp",
  "https://encrypted-tbn2.gstatic.com/images?q=tbn:x", // sharded CDN host
  "https://duckduckgo.com/?q=cats&iar=images",
  "https://duckduckgo.com/duckchat/v1/chat",
  "https://www.bing.com/images/search?q=cats",
  "https://search.yahoo.com/images/search?p=cats",
];
for (const url of shouldBlock) {
  check(`blocks ${url}`, anyBlockMatch(url));
}

const shouldNotBlock = [
  "https://www.google.com/search?q=cats", // plain search: param-injected, not blocked
  "https://duckduckgo.com/?q=cats", // plain DDG search
  "https://duckduckgo.com/about", // regular page on the AI-tab domain
  "https://www.google.com/search?q=xtbm=isch", // param boundary: not tbm=isch
  "https://example.com/?iar=images", // engine params on a non-engine host
];
for (const url of shouldNotBlock) {
  check(`does not block ${url}`, !anyBlockMatch(url));
}

// Param-enforcement redirects must cover the plain search pages.
const paramRegexes = matchers((r) => r.action.redirect?.transform);
check(
  "safe-param transform covers google /search",
  paramRegexes.some((re) => re.test("https://www.google.com/search?q=x"))
);
check(
  "safe-param transform covers duckduckgo",
  paramRegexes.some((re) => re.test("https://duckduckgo.com/?q=x"))
);

// YouTube restricted-mode header rule exists.
check(
  "youtube modifyHeaders rule present",
  rules.some(
    (r) =>
      r.action.type === "modifyHeaders" &&
      r.action.requestHeaders?.[0]?.header === "YouTube-Restrict" &&
      r.action.requestHeaders?.[0]?.value === "Strict"
  )
);

// URL filter: allow outranks block.
const allowRule = rules.find((r) => r.action.type === "allow");
check("allow rule present", !!allowRule);
check(
  "allow priority outranks blocks",
  rules.filter((r) => r.action.type !== "allow").every((r) => r.priority < allowRule.priority)
);

// --- schedule + host helpers -------------------------------------------------

check("empty schedule is always active (fail open)", x.scheduleIsActive({ enabled: true, windows: [] }));
check(
  "overnight window active before midnight",
  x.scheduleIsActive(
    { enabled: true, windows: [{ days: [0], start: "22:00", end: "06:00" }] },
    new Date("2026-07-06T23:30:00") // a Monday
  )
);
check(
  "overnight window active after midnight (next day)",
  x.scheduleIsActive(
    { enabled: true, windows: [{ days: [0], start: "22:00", end: "06:00" }] },
    new Date("2026-07-07T05:30:00") // Tuesday morning
  )
);
check(
  "outside window is inactive",
  !x.scheduleIsActive(
    { enabled: true, windows: [{ days: [0], start: "22:00", end: "06:00" }] },
    new Date("2026-07-06T12:00:00")
  )
);
check("subdomain matches list", x.hostInList("a.b.example.com", ["example.com"]));
check("suffix does not overmatch", !x.hostInList("notexample.com", ["example.com"]));

if (failures) {
  console.error(`rules check: ${failures} failure(s)`);
  process.exit(1);
}
console.log("rules check: all assertions OK");
