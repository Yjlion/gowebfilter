# WebFilter for Firefox

A standalone, self-contained in-browser filter (Manifest V3 WebExtension).
This is **Deliverable 2** of `docs/plans/android-firefox-transparent-mode.md`:
it reproduces the Go proxy's filtering features with browser APIs and
client-side ML — no proxy, no CA certificate, no network round-trips for
classification. It shares *data assets* with the Go project (the Bayes text
model, the NSFW image model family), not code.

## Feature map (Go addon → extension mechanism)

| Go addon | Here | How |
|---|---|---|
| `safesearch` | `background/rules_data.js` → dynamic DNR rules | `declarativeNetRequest` redirect with a query transform (`safe=active`, `adlt=strict`, `kp=1`, `vm=r`), `modifyHeaders` for YouTube Restricted Mode, block rules for the Images/Videos/AI tabs (host-AND-path/param scoped — same DuckDuckGo `/duckchat` and Google `udm=` gotchas as the Go side) and sharded image-CDN hosts |
| `url_filter` (allow/block) | dynamic DNR rules | domain block/allow lists, blacklist or whitelist mode; allow-list hits outrank everything, mirroring the proxy's `URLAllowed` short-circuit. Category lists are not wired yet |
| `doh_filter` | `rules/doh.json` static ruleset | different mechanism, same goal (stop DNS-based bypass): the proxy consults a filtering resolver per-domain; a browser can't, so this blocks requests to known public DoH endpoints instead. **It cannot disable Firefox's own built-in DoH** — that never passes through extension APIs; check Settings → Privacy & Security → DNS over HTTPS |
| `text_classifier` | `content/textfilter.js` + `background/bayes.js` | the same keyword prefilter (3 hits block) and Bayesian scorer, ported 1:1 (`test/bayes_parity.mjs` proves score equality against the Go implementation). Uses visible text (`innerText`) rather than raw HTML — strictly fewer false positives |
| `image_classifier` | `content/imagefilter.js` + `background/nsfw.js` | the same GantMan/nsfw_model MobileNetV2 the proxy embeds, run by TF.js in the background page. Same skin-ratio gate (0.07), same combined score (`porn + hentai + 0.5*sexy`), same threshold/action semantics. Images are blurred eagerly and released when cleared |
| `youtube_filter` | `content/youtube.js` | DOM-level: channel block/allow lists (UC id, @handle, or name), hide home feed / comments / recommendations. The proxy rewrites InnerTube JSON; a content script hides what the page renders |
| policy `schedule` | `background/background.js` | same weekly-window semantics as `internal/models/schedule.go` (0=Monday, overnight windows, fail-open) |
| `quic_blocker`, `policy_router`, `mitm_control`, `proxy_auth`, `mgmt_access` | — | proxy infrastructure; no analogue in a per-browser extension. Firefox handles its own QUIC |

## Install (temporary, for development)

1. Firefox ≥ 128.
2. `about:debugging#/runtime/this-firefox` → **Load Temporary Add-on…** →
   pick `firefox-extension/manifest.json`.
3. Click the toolbar icon to toggle features; **All settings…** opens the
   full options page (engine tabs, lists, thresholds, schedule).

Everything defaults **off** (matching the proxy's policy defaults — NSFW
classifiers have real false-positive cost); the extension is inert until you
enable features.

For a permanent install the extension must be signed (AMO or
`web-ext sign`); that's not set up yet.

## Development

No build step — plain JS, everything vendored (see `NOTICE`). Lint and run:

```bash
npx web-ext lint  -s firefox-extension
npx web-ext run   -s firefox-extension   # launches a Firefox profile with the extension
```

Parity test for the Bayes text scorer (requires Node; regenerate vectors
with Go after model changes):

```bash
go run firefox-extension/test/gen_vectors.go   # from the repo root
node firefox-extension/test/bayes_parity.mjs
```

## Layout

```
firefox-extension/
├── manifest.json               MV3, Firefox event page + DNR
├── rules/doh.json              static DNR ruleset (known DoH endpoints), off by default
├── background/
│   ├── rules_data.js           SafeSearch engine table (port of safesearch.go) + default settings
│   ├── background.js           settings → DNR dynamic rules, schedule, scoring services
│   ├── bayes_model.js          generated from internal/classify/textbayes/model_data.json
│   ├── bayes.js                Bayes scorer + keyword prefilter (port of Go)
│   └── nsfw.js                 TF.js NSFW scoring + skin-ratio gate (port of Go)
├── content/
│   ├── imagefilter.js/.css     eager blur, MutationObserver, size prefilter
│   ├── textfilter.js           visible-text scan → in-place block page
│   └── youtube.js              channel lists + comments/recs/home hiding
├── pages/blocked.html          DNR redirect target for blocked navigations
├── popup/ options/             quick toggles / full settings (browser.storage.local)
├── vendor/                     tf.min.js + mobilenet_v2 model (see NOTICE)
└── test/                       Go→JS Bayes parity vectors + test
```

## What is verified vs. not

- **Verified here:** `web-ext lint` passes; the Bayes port matches the Go
  scorer bit-for-bit on the committed vectors; the vendored TF.js model
  loads and produces a valid 5-class softmax (`[drawing, hentai, neutral,
  porn, sexy]`).
- **Not verified (needs a real Firefox):** the DNR rules against live
  Google/Bing/DDG/Yahoo traffic, image-filter behavior on real pages,
  YouTube DOM selectors (YouTube changes markup frequently — expect these
  to need maintenance), event-page lifecycle under memory pressure, and
  classification latency on low-end hardware.
