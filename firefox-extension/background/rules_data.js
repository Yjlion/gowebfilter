// Per-engine SafeSearch data, ported from the Go proxy's
// internal/proxy/addons/safesearch.go searchEngines table (itself a port of
// the Python original's safesearch.py). Keep the two in sync when an engine
// changes its URL scheme.
//
// hostRe fragments are regexFilter (RE2) host patterns anchored at
// "^https?://"; they intentionally mirror the Go side's matching looseness
// (Go matches any host containing ".google.", so www.google.co.uk works).
// The AI/images/videos tab rules are host-AND-path/param scoped for engines
// whose tabs live on the regular search domain (DuckDuckGo's /duckchat,
// Google's udm= params) — blocking the whole hostname there would break
// normal search. Only genuinely separate AI domains (Gemini, Copilot) are
// blocked at the domain level. See the safesearch gotcha in CLAUDE.md.

"use strict";

// Matches any *.google.<tld> host (and apex google.<tld>).
const GOOGLE_HOST = "([^/]*\\.)?google\\.[^/:]+";
const DDG_HOST = "([^/]*\\.)?duckduckgo\\.com";
const BING_HOST = "([^/]*\\.)?bing\\.com";
const YAHOO_HOST = "([^/]*\\.)?yahoo\\.com";

const SEARCH_ENGINES = {
  google: {
    // safe=active on /search paths.
    safeParam: { key: "safe", value: "active" },
    paramHostRe: GOOGLE_HOST,
    paramPathRe: "/search",
    // Google's unified nav selects tabs via "udm" (2=Images, 7=Videos,
    // 50=AI Mode); "tbm" is the legacy scheme, still honored for old links.
    imagesTab: {
      pathRes: [`${GOOGLE_HOST}/imghp`],
      paramRes: [
        `${GOOGLE_HOST}/[^#]*[?&]tbm=isch(&|#|$)`,
        `${GOOGLE_HOST}/[^#]*[?&]udm=2(&|#|$)`,
      ],
      // Google shards thumbnail hosts encrypted-tbn0..N.gstatic.com; an
      // exact-match list silently misses most real traffic.
      cdnRes: [`encrypted-tbn[^./]*\\.gstatic\\.com`],
    },
    videosTab: {
      pathRes: [`${GOOGLE_HOST}/videohp`],
      paramRes: [
        `${GOOGLE_HOST}/[^#]*[?&]tbm=vid(&|#|$)`,
        `${GOOGLE_HOST}/[^#]*[?&]udm=7(&|#|$)`,
      ],
    },
    aiTab: {
      domains: ["gemini.google.com", "bard.google.com"],
      paramRes: [`${GOOGLE_HOST}/[^#]*[?&]udm=50(&|#|$)`],
    },
  },

  bing: {
    safeParam: { key: "adlt", value: "strict" },
    paramHostRe: BING_HOST,
    paramPathRe: "/search",
    imagesTab: {
      pathRes: [`${BING_HOST}/images/`],
      cdnDomains: ["th.bing.com"],
    },
    videosTab: {
      pathRes: [`${BING_HOST}/videos/`],
    },
    aiTab: {
      domains: ["copilot.microsoft.com"],
    },
  },

  duckduckgo: {
    // kp=1 on every path — DDG carries the safe-search flag on all requests.
    safeParam: { key: "kp", value: "1" },
    paramHostRe: DDG_HOST,
    paramPathRe: "/",
    imagesTab: {
      paramRes: [`${DDG_HOST}/[^#]*[?&]iar=images(&|#|$)`],
    },
    videosTab: {
      paramRes: [`${DDG_HOST}/[^#]*[?&]iar=videos(&|#|$)`],
    },
    aiTab: {
      // DDG's AI Chat lives at /duckchat on duckduckgo.com itself, so it
      // must be scoped by path rather than blocking the whole domain.
      pathRes: [`${DDG_HOST}/duckchat`],
    },
  },

  yahoo: {
    safeParam: { key: "vm", value: "r" },
    paramHostRe: YAHOO_HOST,
    paramPathRe: "/search",
    imagesTab: {
      pathRes: [`${YAHOO_HOST}/images/search`],
    },
    videosTab: {
      pathRes: [`${YAHOO_HOST}/video/search`],
    },
  },

  youtube: {
    // Header-based enforcement: YouTube Restricted Mode, all paths.
    safeHeader: { name: "YouTube-Restrict", value: "Strict" },
    headerDomains: ["youtube.com", "m.youtube.com", "music.youtube.com", "youtu.be"],
  },
};

// Default settings, mirroring the Go side's policies/default.json.example
// where features map 1:1. Everything defaults off (matching the proxy: NSFW
// false positives have real cost), except the extension itself is "on" so
// enabling a feature takes effect immediately.
const DEFAULT_SETTINGS = {
  enabled: true,
  urlFilter: { enabled: false, mode: "blacklist", allow: [], block: [] },
  safesearch: {
    enabled: false,
    engines: {
      google: { enabled: true, blockImagesTab: false, blockVideosTab: false, blockAiTab: false },
      bing: { enabled: true, blockImagesTab: false, blockVideosTab: false, blockAiTab: false },
      duckduckgo: { enabled: true, blockImagesTab: false, blockVideosTab: false, blockAiTab: false },
      yahoo: { enabled: true, blockImagesTab: false, blockVideosTab: false, blockAiTab: false },
      youtube: { enabled: true },
    },
  },
  doh: { blockEndpoints: false },
  textClassifier: { enabled: false, threshold: 0.8 },
  imageClassifier: { enabled: false, action: "blur", threshold: 0.75, minSize: 64 },
  youtube: {
    enabled: false,
    mode: "blacklist",
    channels: [],
    blockHome: true,
    removeComments: false,
    removeRecommendations: false,
  },
  // Fail-open like the Go side: disabled or empty-window schedules mean
  // "always active". Days use 0=Monday..6=Sunday (ISO weekday - 1).
  schedule: { enabled: false, windows: [] },
};
