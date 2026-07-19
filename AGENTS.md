# AGENTS.md

Guidance for Codex or any AI agent working in this repo.

## What this is

A from-scratch Go port of `mitmproxy-web-filter` (Python/mitmproxy): a
MITM-intercepting forward proxy with per-client filtering policies and a
browser-based management UI, all in one static binary. Full history,
architecture rationale, and what is verified vs. not lives in
[HANDOFF.md](HANDOFF.md); read that before structural changes.

This project was built primarily through AI-assisted sessions. Do not
assume behavior was deliberately hand-verified against real-world traffic
unless HANDOFF.md or a test explicitly says so.

## Build / test / run

Both classifiers are embedded and pure Go. The project should build with
`CGO_ENABLED=0`; no ONNX Runtime DLL, GoMLX backend, Python environment, or
C compiler is required for normal builds.

```bash
CGO_ENABLED=0 go build ./...
go vet ./...
go test ./...

go build -o webfilter.exe ./cmd/webfilter
./webfilter.exe run --settings config/settings.json
```

`config/settings.json` and `policies/*.json` are gitignored runtime state.
Copy from `config/settings.example.json` / `policies/default.json.example`
for local dev. They persist to disk; the mgmt API's
`PUT /api/policies/{name}` writes straight through to
`policies/{name}.json`.

## Layout

- `cmd/webfilter/` - cobra CLI (`run`/`proxy`/`mgmt`/`tray`/`gui`/
  `categories update`/`oui update`)
- `cmd/webfilter/internal/gui/` - native desktop management UI
  (github.com/gogpu/ui, pure Go/WebGPU, CGO_ENABLED=0). Deliberately under
  `cmd/`, not top-level `internal/`, so the Android sweep
  (`GOOS=android go build ./mobile ./internal/...`) never compiles the gogpu
  windowing stack. `mgmtclient/` (typed loopback HTTP client) and `uimodel/`
  (headless view-models) hold the logic and tests; widget files are thin.
  `webfilter gui` self-hosts the engine when the mgmt port is free (tray
  pattern) or attaches over HTTP otherwise; closing the window only stops a
  self-hosted engine. No build tags - headless Linux simply never runs the
  `gui` command (X11/Wayland is a runtime need of that command only).
- `internal/app/` - shared engine wiring (`BuildProxyEngine`, classifier loaders,
  `EnsureTunSocksListener`, `ServeMgmt`); the fixed addon pipeline order is
  single-sourced here and reused by both `cmd/webfilter` and `mobile/`
- `internal/models/` - `Policy`/`GlobalSettings` structs + JSON schema
- `internal/proxy/` - MITM engine, pipeline, block-page rendering
- `internal/proxy/state/` - hot-reloaded settings/policies and policy routing
- `internal/proxy/addons/` - filtering addons, wired in fixed order in `internal/app/engine.go`
- `internal/mgmtapi/` - chi router, REST API, embedded UI static serving
- `internal/settingsvc/` - settings/policy merge + validation shared by
  `PUT /api/settings` and the `mobile/` native-settings path, plus the MDM
  managed-config apply (`ApplyManagedConfig`)
- `internal/classify/textbayes/` - embedded pure-Go Bayesian adult-text scorer
- `internal/classify/image/` - embedded pure-Go GantMan/nsfw_model image classifier
- `mobile/` - gomobile-bound Android entry point (`Start`, `StartProxyOnly`
  (no-TUN loopback-proxy/PAC mode), `Stop`, `Status`, …, plus JSON-string
  exports per concern: `settingsapi.go`, `managed.go`, `policiesapi.go`,
  `categoriesapi.go`, `logsapi.go`);
  drives tun2socks from the VpnService `fd://` TUN. Build with
  `gomobile bind -target=android/arm64,android/arm,android/amd64 -androidapi 26 -o android/app/libs/webfilter.aar ./mobile`.
  Before binding for `android/amd64` (x86_64 emulators), run
  `go run scripts/patch_libc_seccomp.go` — `modernc.org/libc`'s musl syscall
  dispatchers issue legacy path-based syscall numbers that Android's x86_64
  app seccomp policy kills; the script remaps them to the *at family via a
  gitignored libc copy + a go.mod `replace` (`-undo` reverts; never commit
  the replace). arm64 is unaffected.
- `android/` - Kotlin/Gradle Android app (VpnService with a proxy-only
  no-TUN mode, WebView mgmt UI, native settings/policies/categories/logs/
  analytics screens, MDM managed configurations, per-app filtering, CA
  install/save flow) consuming the gomobile AAR. Debug APKs build via
  the `.github/workflows/android.yml` workflow (manual trigger, and on `v*`
  tags from `ci.yml`'s release job, which attaches the APK to the release)
- `firefox-extension/` - standalone MV3 Firefox WebExtension (no proxy/CA):
  declarativeNetRequest for SafeSearch/URL/DoH, ported Bayes text scorer,
  vendored TF.js NSFW model. Verify with `npx web-ext lint -s firefox-extension`
  and `node firefox-extension/test/{bayes_parity,rules_check}.mjs`; regenerate
  `bayes_model.js`/`bayes_vectors.json` (`test/gen_vectors.go`) whenever the Go
  textbayes model changes
- `ui/` - management web UI copied from the Python original; Alpine.js and
  qrcodejs are vendored (`ui/alpine.min.js`, `ui/qrcode.min.js`) for offline use

## Known gotchas

- Policy selection is by source IP/MAC, first match wins, tiered
  MAC -> exact-IP -> CIDR -> catch-all. Check `GET /api/policies` before
  assuming the `default` policy applies.
- Blocked responses return HTTP 200 with a block-page body, not 4xx. Check
  `GET /api/logs?kind=requests` or `?kind=blocks` to verify filtering.
- SafeSearch engine matching is host-and-path/param scoped for engines
  whose AI/images/videos tabs share a domain with regular search.
- Google shards image-CDN thumbnail hosts (`encrypted-tbn0` through at
  least `encrypted-tbn3.gstatic.com`); use prefix/suffix matching for that
  hostname family.
- Response bodies reaching addons are identity-encoded. The engine strips
  the client's `Accept-Encoding` before the upstream fetch so content
  inspectors do not scan compressed bytes.
- NSFW images are also embedded as `data:image/...` URIs inside HTML/CSS/JS
  and JSON; image classifier inline scanning handles those.
- Settings changes need a restart; policy changes hot-reload.
- Never unmarshal a partial policy body over an existing policy: sub-config
  `UnmarshalJSON` resets to defaults first and wipes sibling fields. Use
  `settingsvc.MergePolicyPatch` or full-document writes.
- The MDM settings lock is `config/managed.json` (written only by
  `settingsvc.ApplyManagedConfig`, re-read per request). Desktop never
  writes it; missing file = unlocked. New mutating mgmt routes must take
  the `requireUnlocked` middleware or `TestMutatingRoutesAreLockGated`
  fails.
- Android restriction bundles have no float type: thresholds are string
  restrictions; the models' decoders accept string-typed numbers. Keep
  `app_restrictions.xml`, `PreferenceDataStores.kt`, and
  `ManagedConfig.buildDocFromBundle` key sets in sync. Documented
  exceptions: `proxy_only_mode` is Kotlin-only (service start mode, not
  engine config); `url_filter_categories` is edited by CategoriesActivity,
  not a preference widget.
- The PAC file advertises the first `regular` listener
  (`PrimaryRegularProxyPort`, 8080 fallback) — never a SOCKS port.
  Proxy-only mode (`mobile.StartProxyOnly`) injects a session-only
  `regular@127.0.0.1:8080` (`app.EnsureLocalHTTPProxyListener`), never
  persisted to settings.json.
- Category lists may be stored gzip-compressed: the store prefers
  `<name>/domains.gz`; per-category downloads
  (`categories.DownloadCategory`, `https://dbl.ipfire.org/lists/<name>/domains.txt`)
  write `.gz`, the tarball CLI still writes plain files. Sets over 100k
  domains are sorted 64-bit hashes (over-block-only collision risk), not
  string maps.
- Read the log DB from exports via `logstore.NewReader` (write-free), never
  a second `logstore.Configure` (competing writer + schema + prune).
- The "default" policy cannot be deleted or renamed through the mobile
  exports (always-on fallback; MDM `policy_json` targets it by name);
  `CreatePolicyJson` defaults new policies to `inactive:true`.
- Do not reintroduce `http.FileServer`/`FileServerFS` for the UI static
  path; it caused a `/` <-> `/index.html` redirect loop.
- Test helpers constructing a `Server`/`CA`/`PolicyStore`/log store directly
  must seed absolute temp-dir paths for `cert_dir`/`policies_dir`/`logs_dir`.
- The text classifier has no model path anymore. `text_classifier_model_path`
  is deprecated and ignored for backward-compatible settings round trips.
- The native desktop GUI is an HTTP client of the mgmt API even when it
  self-hosts the engine - all reads/writes go through
  `cmd/webfilter/internal/gui/mgmtclient` to loopback HTTP, never directly
  to disk or a second SQLite handle. Self-host auth uses
  `mgmtapi.Server.SessionCookie()`; the supervisor re-seeds it after every
  engine restart.
- gogpu/ui is pinned at v0.x with API churn between minors; verify widget
  option names against the module cache on upgrades (they diverge from the
  docs' examples, e.g. `primitives.CrossAxisCenter`,
  `textfield.TypePassword`) and re-run `webfilter gui` manually.
- The GUI drives its own render loop (`gui.runRenderLoop`), not
  `desktop.Run`, because v0.1.44's compositor double-applies the DPI scale
  (2.25x/cropped on a 150% display) and never clears (old-tab bleed-through).
  Several non-obvious rules keep it correct/efficient; don't regress them:
  (1) clear the whole gg canvas every frame and apply the DPI scale exactly
  once via `cc.Scale` on a physical-pixel `ggcanvas.NewWithScale(..., 1.0)`
  canvas; (2) call `Window().HandleResize` ONLY when the size changed — every
  frame keeps the 30fps anim pumper alive and pegs a CPU core idle; (3) lists
  use `gui.scrollBox` (plain clip+translate), never `core/listview` (renders
  blank in this loop) or `core/scrollview` (self-invalidates ~100fps once
  overflowing); (4) the gg GPU accelerator (`_ "github.com/gogpu/gg/gpu"`) is
  imported by `cmd/webfilter/cmd_gui.go`, NOT the `gui` package, or the
  `offscreen` snapshot test renders blank; (5) `gui.redraw()`/`onTabSelected`
  mark the root needs-layout so async data and tab switches re-lay-out.
- The tab strip is the custom `gui.tabBar` (icon+label, drawn via the canvas
  `RenderSVG` path with `github.com/gogpu/ui/icon`), not `core/tabview` —
  tabview tabs are bare label strings and cannot show icons. The tab bar only
  sets `activeTab`; the content switch is `contentSwap.SetChild(...)` in
  `onTabSelected`, so programmatic tab selection (snapshot tests) must do
  both. The Advanced tab (proxy auth, upstream proxy, tun2socks) is a second
  view over the settings screen's shared form state — one save/reload path.
- OS clipboard access is `ui.copyText` → `gogpu.App.ClipboardWrite`; the ui
  textfield's Ctrl+C "clipboard" is an internal placeholder and there is no
  selectable-text widget, hence the click-to-copy log rows.
- Snapshot-test the five screens headlessly with
  `GUI_SNAPSHOT_DIR=<dir> go test ./cmd/webfilter/internal/gui -run TestRenderSnapshots`
  (writes dashboard/policies/logs/settings/advanced PNGs; skipped without the env).

## Live testing

Verify behavior via the mgmt API and request/block logs, not just curl
status codes:

```bash
curl -s http://127.0.0.1:8000/api/policies
curl -s http://127.0.0.1:8000/api/policies/<name>
curl -s -X PUT http://127.0.0.1:8000/api/policies/<name> -d @updated.json
curl -s "http://127.0.0.1:8000/api/logs?kind=requests&limit=20"
curl -s "http://127.0.0.1:8000/api/logs?kind=blocks&limit=20"
```

If you temporarily change a live policy for testing, restore it before
finishing.
