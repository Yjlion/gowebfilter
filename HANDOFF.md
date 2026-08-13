# Handoff: Go port of mitmproxy-web-filter

Status: the project is a single-binary Go web-filtering proxy with
management UI, MITM interception, policy routing, filtering addons, and
embedded NSFW classifiers. Both classifiers are pure Go and embedded:

- `internal/classify/image`: GantMan/nsfw_model (MobileNetV2,
  MIT-licensed), embedded as `model.bin` and executed by the local pure-Go
  inference engine.
- `internal/classify/textbayes`: compact embedded Bayesian adult-text
  scorer. It implements `addons.MLScorer`, needs no model directory, and
  does not use ONNX Runtime, GoMLX, Python, CGO, or a sidecar DLL.

`CGO_ENABLED=0 go build ./...`, `go vet ./...`, and `go test ./...` are the
expected verification commands after changes.

## Architecture

- One binary: `webfilter run|proxy|mgmt|tray|gui|categories update|oui update|version`.
- `run` starts the proxy engine and management server together; `proxy` and
  `mgmt` remain available for process isolation.
- `proxy_listen` supports `regular@host:port` (plaintext HTTP proxy),
  `socks4@host:port` (SOCKS4/4a), and `socks5@host:port`, each optionally
  wrapped in TLS: `https@` (== `tls+regular@`, an HTTP proxy over TLS /
  "Secure Web Proxy"), `tls@` (== `tls+socks5@`, SOCKS5 over TLS), or the
  general `tls+<base>@` prefix. TLS-wrapped listeners present a leaf minted on
  the fly by the runtime CA (SNI, or the connected-to address for SNI-less
  IP-literal endpoints), so a client that trusts the CA for MITM also trusts
  the proxy endpoint. Unsupported modes are skipped with a warning. Parsing is
  `models.ParseListenSpec` (`internal/models/proxylisten.go`); TLS termination
  and mode dispatch happen in `Engine.dispatchConn`
  (`internal/proxy/engine.go`).
- SOCKS5 support lives in `internal/proxy/socks5.go` and SOCKS4/4a in
  `internal/proxy/socks4.go`. Both implement CONNECT only and join the same
  tunnel path as HTTP CONNECT. SOCKS5 supports no-auth and username/password
  auth through the existing `ProxyAuthGate` and also serves a DNS-only UDP
  ASSOCIATE relay; SOCKS4 has no password channel, so an auth-required proxy
  refuses SOCKS4 clients. BIND is rejected. HTTPS through any of these is
  raw-spliced when MITM is unavailable or bypassed, and MITM-filtered when a
  runtime CA is available.
- Config and state live on disk: `config/settings.json`, `policies/*.json`,
  `certs/`, `categories/`, `logs/webfilter.db`, and `data/`.
- Settings changes need a restart. Policy changes hot-reload.
- The management UI is served from embedded `ui/` files. Do not switch it
  back to `http.FileServer`/`FileServerFS`; that caused an index redirect
  loop.
- **Native desktop GUI** (`webfilter gui`, `cmd/webfilter/internal/gui/`,
  github.com/gogpu/ui — pure Go, WebGPU, keeps `CGO_ENABLED=0`): dashboard,
  policy editor, log viewer, settings, and an Advanced tab (proxy client
  authentication, upstream proxy, full tun2socks config — a second view over
  the settings screen's shared form/save state); everything else defers to an
  "Open Web UI" button. Deliberate decisions, in case someone is tempted to
  revisit them:
  - **It is an HTTP client of the mgmt API, always** — even when it
    self-hosts the engine in-process (which it does, tray-style, when the
    mgmt port is free). Typed in-process writes were rejected because they
    would be a third copy of the write-coherence rules (after mgmtapi and
    mobile/) and would break when a separate process owns the engine.
    Self-host mode authenticates by seeding the client with
    `mgmtapi.Server.SessionCookie()` instead of prompting the local owner.
  - **No build tags.** The toolkit compiles into every target (~19 MB
    binary growth); headless Linux is unaffected because only the `gui`
    command touches a display. A `noui` tag split was considered and
    rejected for CI-matrix cost.
  - The GUI package lives under `cmd/webfilter/internal/` specifically so
    `GOOS=android go build ./mobile ./internal/...` never compiles gogpu.
  - gogpu/ui is v0.x and young; the widget layer is deliberately thin over
    the headless, tested `uimodel`/`mgmtclient` packages so API churn stays
    contained. Real-world text-input/IME quality is unproven — if it
    disappoints, free-text editing degrades to the web UI.
  - Closing the window exits the process, stopping a self-hosted engine
    (the header says which mode you're in); the tray's "Open Native UI"
    item spawns `gui` as a separate process, which attaches and can be
    closed freely.
  - **Rendering drives gogpu directly (`gui.runRenderLoop`), not
    `desktop.Run`.** gogpu/ui v0.1.44's compositor was unusable here on two
    counts, both found by rendering the window to PNG and inspecting it:
    it double-applied the DPI scale (2.25×, cropped on a 150% display) and
    its per-boundary GPU textures never cleared, so a previous tab's content
    bled through on tab switch. The custom loop clears the whole gg canvas
    every frame and applies the DPI scale exactly once (`cc.Scale` on a
    physical-pixel canvas; gg's own `WithDeviceScale>1` scales twice in
    v0.50.5) — the same stateless full-repaint the `offscreen` snapshot
    renderer uses. Hard-won gotchas along the way (all in CLAUDE.md): call
    `HandleResize` only on real size change (every-frame pegs a CPU core via
    the 30fps anim pumper); avoid `core/listview` (renders blank in this loop)
    and `core/scrollview` (self-invalidates ~100fps when overflowing) in favor
    of the tiny `gui.scrollBox`; and `redraw()`/`onTabSelected` must mark the
    root needs-layout so async data and tab switches re-lay-out. The tab
    strip is the custom `gui.tabBar` (icon+label via `github.com/gogpu/ui/icon`
    and the canvas `RenderSVG` path) because `core/tabview` tabs are bare
    label strings; content switching is `contentSwap.SetChild` in
    `onTabSelected`. Log rows are click-to-copy (`ui.copyText` →
    `gogpu.App.ClipboardWrite`) because gogpu/ui has no selectable-text
    widget and the textfield's Ctrl+C buffer never reaches the OS clipboard.
    Verify layout
    changes with the headless `offscreen` snapshot test
    (`GUI_SNAPSHOT_DIR=<dir> go test ./cmd/webfilter/internal/gui -run TestRenderSnapshots`)
    and idle CPU by watching the process after data loads (should be ~0%).

## Proxy Pipeline

`internal/app/engine.go`'s `BuildProxyEngine` wires addons in fixed order:

`ManagementAccess -> ProxyAuthGate -> PolicyRouter -> MitmControl ->
UrlFilter -> QuicBlocker -> DohFilter -> SafeSearch -> YouTubeFilter ->
TextClassifier -> ImageClassifier -> RequestLogger`

Order matters. Request hooks still run after an earlier hook sets
`fc.Response`; only the upstream fetch is skipped. This wiring is
single-sourced in `internal/app` so both `cmd/webfilter` (desktop CLI) and
`mobile/` (Android gomobile bindings) construct an identical pipeline —
change the order there, never per front-end.

## Android port (in progress)

Deliverable 1 of `docs/plans/android-firefox-transparent-mode.md` is
scaffolded: the pure-Go engine runs on-device, embedded via gomobile.

- `mobile/` is the gomobile-bound entry point. `Start(dataDir, tunFd)`
  bootstraps mobile settings (loopback mgmt on 127.0.0.1, single SOCKS5
  listener, tun2socks enabled), calls `app.BuildProxyEngine`, serves the
  engine + mgmt server, and funnels the VpnService TUN into the in-process
  SOCKS5 listener via `xjasonlyu/tun2socks`'s `fd://` device. It does **not**
  use `internal/tun2socks.Manager` (root/`ip`-gated).
- `android/` is a Kotlin/Gradle app (VpnService, WebView mgmt dashboard,
  per-app filtering with app icons, CA-install flow). The AAR
  (`android/app/libs/webfilter.aar`) is a build artifact — see
  `android/README.md`.
- **Native settings GUI:** `SettingsActivity` (androidx.preference) edits a
  policy's filters and the mobile-relevant globals through gomobile JSON
  exports (`mobile/settingsapi.go`), disk-backed so it works
  with the VPN stopped. The merge/validation logic shared with
  `PUT /api/settings` lives in `internal/settingsvc` — the two paths are
  deliberately byte-identical. Policy writes are always full documents
  (partial bodies get reset-to-defaults by the sub-config unmarshalers).
- **Native UI second wave** (same disk-backed export conventions):
  proxy-only mode (`mobile.StartProxyOnly` — engine + mgmt with no TUN; a
  session-only `regular@127.0.0.1:8080` listener plus the fixed
  `/proxy.pac` port selection make the PAC usable by an MDM-pushed Chrome
  ProxySettings policy); a multi-policy manager (`mobile/policiesapi.go`;
  "default" is the protected schedule-less fallback, scheduled policies
  override it via the existing catch-all schedule precedence); per-category
  ipfire blocklist downloads (`internal/categories/download.go`, stored as
  `domains.gz`, large sets held as sorted 64-bit hashes); and native
  logs/analytics over the write-free `logstore.Reader`
  (`mobile/logsapi.go`). DoH presets, SafeSearch/YouTube brand icons, and
  the CA save-to-file flow are Kotlin/XML-only.
- **MDM/EMM managed configurations:** `res/xml/app_restrictions.xml` declares
  typed restriction keys plus `policy_json`/`schedule_json` escape hatches
  and a `settings_locked` flag; `ManagedConfig.kt` translates the
  RestrictionsManager bundle into a canonical doc applied by
  `mobile.ApplyManagedConfigJson` → `settingsvc.ApplyManagedConfig`
  (hash-idempotent, validate-before-write). The lock is enforced **in the Go
  mgmt API** (`requireUnlocked` middleware, 403 on config mutations,
  state in `config/managed.json`), not just hidden in the UI — see the
  CLAUDE.md gotchas. Restrictions apply on app start, before engine start,
  and on the (runtime-registered) restrictions-changed broadcast.

**Verified:** `mobile/` and all `internal/...` cross-compile for
`android/arm64` and `android/arm` (`GOOS=android GOARCH=arm64 CGO_ENABLED=0
go build ./mobile ./internal/...`); `go test ./mobile` passes on the host;
the `fd://` scheme exists in the pinned `xjasonlyu/tun2socks v2.6.0`.

**Not verified (needs a real device/emulator):** `modernc.org/sqlite` under
the Android runtime, on-device image-CNN latency/battery, the full
VpnService→tun2socks→engine data path, and the Kotlin app's runtime behavior
(the Kotlin sources compile — the debug APK has been assembled locally —
but the native settings screens and the MDM flow have not been exercised
on a device; `android/README.md` has a TestDPC verification recipe).
Also unverified: whether managed Chrome actually honors a loopback PAC
pushed via its `ProxySettings` app restriction (the proxy-only mode's
intended consumer), and the categories RAM claim (~8 MB for the ~1M-domain
porn list via the hash-set representation) has not been measured on a
device.

**Verified on the x86_64 emulator (2026-07-10):** proxy-only mode end to
end (no VPN consent/key icon, PAC serves `PROXY 127.0.0.1:8080`, requests
through the loopback HTTP proxy are filtered and logged), the native
policies/categories/logs/analytics/DoH-preset screens, a real per-category
ipfire download (`ads` → `domains.gz` on device → category block with
`component=url_filter`), and the CA save-to-Downloads flow. One on-device
fix came out of it: `startForeground` with the `systemExempted` type is
rejected on API 34 unless the app is the active VPN, so proxy-only mode
declares and uses the `specialUse` FGS type instead.

Building the AAR + debug APK is automated in
`.github/workflows/android.yml` — a **manual-only** (`workflow_dispatch`)
workflow that runs gomobile + Gradle on a GitHub runner and uploads both as
artifacts. `ci.yml`'s cross-compile matrix also covers
`GOOS=android GOARCH=arm64` for `./mobile ./internal/...` on every push/PR.

## Firefox extension (in progress)

Deliverable 2 of `docs/plans/android-firefox-transparent-mode.md` is
scaffolded in `firefox-extension/`: a standalone MV3 WebExtension (plain JS,
no build step) reproducing the filters with browser APIs — no proxy, no CA.
See its README for the full Go-addon → extension mechanism map.

- SafeSearch/URL-filter/DoH-bypass via `declarativeNetRequest`; the engine
  table (`background/rules_data.js`) is a port of `safesearch.go`, including
  the same-domain AI/images-tab scoping and sharded-CDN handling.
- The adult-text scorer is the same Bayes model (generated
  `background/bayes_model.js`); `test/bayes_parity.mjs` replays Go-generated
  vectors (`test/gen_vectors.go`) and requires exact score agreement.
- The NSFW image filter runs the same GantMan MobileNetV2 family via
  vendored TF.js (converted from the nsfwjs npm package's bundled model —
  see `firefox-extension/NOTICE`), with the Go side's skin-ratio gate (0.07)
  and combined score (`porn + hentai + 0.5*sexy`) ported verbatim.

**Verified:** `web-ext lint` 0 errors; Bayes JS↔Go score parity on committed
vectors; DNR rule compilation asserted against sample URLs
(`test/rules_check.mjs`); the vendored model loads in TF.js and produces a
valid 5-class softmax.

**Not verified (needs a real Firefox):** DNR behavior against live search
engines, image/YouTube filtering on real pages (YouTube DOM selectors will
need maintenance), event-page lifecycle, low-end classification latency.

## Classifiers

- Text classification is opt-in per policy through `text_classifier.enabled`.
  The addon keeps the high-confidence keyword prefilter and then uses the
  embedded Bayesian scorer for lower-keyword adult text. The policy
  `text_classifier.threshold` still controls blocking sensitivity.
- Tiny HTML pages with multiple high-confidence adult keyword hits block
  immediately; the 100-character floor only protects weak/ambiguous text from
  Bayesian scoring noise.
- `text_classifier_model_path` is deprecated and ignored. It remains in the
  settings struct only for backward-compatible JSON round trips.
- The Bayesian scorer's seed vocabulary is curated from LDNOOBW English list
  concepts with CC-BY-4.0 attribution; see
  `internal/classify/textbayes/NOTICE`. e2guardian and Redwood lists were
  treated as references only, not embedded data.
- Image classification is opt-in per policy through `image_classifier.enabled`
  and uses the embedded MobileNetV2 model. The image addon also scans inline
  `data:image/...` URIs in HTML/CSS/JS/JSON bodies.

## Known Gotchas

- Policy selection is source based, first match wins, tiered
  MAC -> exact IP -> CIDR -> catch-all.
- Blocked responses return HTTP 200 with a block-page body. Verify filtering
  through request/block logs, not status code alone.
- Response bodies reaching addons are identity-encoded because the engine
  strips the client's `Accept-Encoding` before upstream fetch.
- SafeSearch matching must account for engines where AI/images/videos tabs
  share the normal search hostname.
- Google image thumbnails use sharded hosts such as
  `encrypted-tbn0.gstatic.com` through at least `encrypted-tbn3.gstatic.com`.
- Tests that construct config-backed services directly must use absolute
  temp paths for `cert_dir`, `policies_dir`, and `logs_dir`.
- Local `main` may lag GitHub because fixes have been landing through PRs.
  Before publishing, fetch `origin/main` and reconcile with it so newer
  diagnostics, OUI, PAC, SOCKS, or policy-editor fixes are not dropped.

## Verification Notes

Useful focused checks after classifier or wiring changes:

```bash
CGO_ENABLED=0 go test ./internal/classify/textbayes ./internal/proxy/addons ./internal/app
CGO_ENABLED=0 go test ./internal/proxy
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -o webfilter.exe ./cmd/webfilter

# Android build must stay green after mobile/ or internal/app changes:
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build ./mobile ./internal/...
CGO_ENABLED=0 go test ./mobile
```

When testing a live instance, inspect:

```bash
curl -s http://127.0.0.1:8000/api/policies
curl -s "http://127.0.0.1:8000/api/logs?kind=requests&limit=20"
curl -s "http://127.0.0.1:8000/api/logs?kind=blocks&limit=20"
```
