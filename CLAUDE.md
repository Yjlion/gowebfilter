# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A from-scratch Go port of `mitmproxy-web-filter` (Python/mitmproxy): a
MITM-intercepting forward proxy with per-client filtering policies and a
browser-based management UI, all in one static binary. Full history,
architecture rationale, and what's verified-vs-not lives in
[HANDOFF.md](HANDOFF.md) — read that before making structural changes.
This file is the short version for day-to-day work.

**This project was built primarily through AI-assisted sessions** (see the
disclaimer in [README.md](README.md)). Don't assume any given piece of
behavior was deliberately hand-verified against real-world traffic unless
HANDOFF.md or a test explicitly says so.

## Build / test / run

Both NSFW classifiers are pure Go with embedded models — **no CGO, ONNX
Runtime, Python environment, or C toolchain is required**. Build with
`CGO_ENABLED=0` (the SQLite log store is `modernc.org/sqlite`, also pure Go).

```bash
CGO_ENABLED=0 go build ./...
go vet ./...
go test ./...

# focused checks after classifier or pipeline-wiring changes:
go test ./internal/classify/textbayes ./internal/proxy/addons ./internal/app
go test ./internal/proxy

# single test:
go test ./internal/proxy/addons -run TestSafeSearch -v

# after touching mobile/ or shared engine wiring, confirm the Android build:
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build ./mobile ./internal/...
go test ./mobile

go build -o webfilter ./cmd/webfilter             # (webfilter.exe on Windows)
./webfilter run --settings config/settings.json   # proxy (:8080, SOCKS5 :1080) + mgmt UI (:8000) in one process
```

CLI commands: `run` (proxy + mgmt together), `proxy` / `mgmt` (standalone
for process isolation), `tray` (desktop system-tray controller — self-hosts
the proxy+mgmt server if nothing is already listening on the mgmt port),
`service` (Windows service management), `categories update`, `oui update`,
`version`.

`config/settings.json` and `policies/*.json` are gitignored runtime state —
first start bootstraps them from `config/settings.example.json` /
`policies/default.json.example`. They persist to disk; the mgmt API's
`PUT /api/policies/{name}` writes straight through to `policies/{name}.json`.
Request/block/audit logs go to SQLite at `logs/webfilter.db`.

## Layout

- `cmd/webfilter/` — cobra CLI; `runners.go` delegates engine construction to
  `internal/app`. The desktop-only tun2socks manager (`runEngineWithTun`)
  stays here because it is root/`ip`-gated and OS-coupled.
- `internal/app/` — **single-sources the engine wiring** shared by the CLI and
  the Android `mobile/` package. `BuildProxyEngine` wires the addon pipeline in
  a **fixed order** that matters (mirrors the Python original):
  `ManagementAccess → ProxyAuthGate → PolicyRouter → MitmControl → UrlFilter →
  QuicBlocker → DohFilter → SafeSearch → YouTubeFilter → TextClassifier →
  ImageClassifier → RequestLogger`. Request hooks still run after an earlier
  hook sets `fc.Response`; only the upstream fetch is skipped. Also holds
  `LoadTextScorer`/`LoadImageDetector`, `EnsureTunSocksListener`, and
  `ServeMgmt`. **Edit the pipeline order here, not per front-end.**
- `mobile/` — gomobile-bound Android entry point. Exports a small API surface
  (`Start(dataDir, tunFd)`, `Stop`, `IsRunning`, `Status`, `MgmtUrl`,
  `ReloadPolicies`, `CaCertPem`, plus the JSON-string settings/policy
  accessors for the native settings UI and MDM path in `settingsapi.go` /
  `managed.go`: `GetSettingsJson`, `UpdateSettingsJson`, `GetPolicyJson`,
  `UpdatePolicyJson`, `GetManagedStateJson`, `ApplyManagedConfigJson`) and
  drives `xjasonlyu/tun2socks` directly from
  the VpnService `fd://` descriptor — **not** via `internal/tun2socks.Manager`
  (root/`ip`-gated). The TUN-capture file is `tun_capture.go`
  (`//go:build android || linux`), deliberately **not** named `*_android.go`:
  a GOOS filename suffix ANDs with the build tag and would exclude it on a
  Linux desktop, breaking `go test ./mobile`. Build:
  `gomobile bind -target=android/arm64,android/arm -androidapi 26 -o android/app/libs/webfilter.aar ./mobile`.
- `android/` — Kotlin/Gradle app scaffold (VpnService, WebView mgmt UI, native
  settings screens backed by the `mobile/` JSON API, per-app filtering with
  app icons, MDM managed configurations via `app_restrictions.xml` +
  `ManagedConfig.kt`, CA install flow). See `android/README.md` for local
  build steps and the TestDPC verification recipe;
  the AAR is a build artifact (gitignored). The debug APK can also be built on
  demand by the **manual** `.github/workflows/android.yml` workflow
  (`workflow_dispatch` only — Actions tab → "Android APK" → Run workflow;
  artifacts: APK + AAR).
- `internal/settingsvc/` — settings/policy merge + validation shared by
  `PUT /api/settings` and the `mobile/` native path (they must behave
  byte-identically), plus the managed-config apply logic
  (`ApplyManagedConfig`).
- `firefox-extension/` — standalone MV3 Firefox WebExtension (plan doc
  Deliverable 2): reproduces the filters with browser APIs + client-side ML —
  no proxy, no CA, plain JS with no build step. SafeSearch/URL/DoH via
  `declarativeNetRequest` (`background/rules_data.js` ports `safesearch.go`'s
  engine table — keep the two in sync), the same Bayes text scorer (generated
  `background/bayes_model.js`; `test/bayes_parity.mjs` + `test/gen_vectors.go`
  prove score equality with the Go implementation — regenerate vectors after
  model changes), and the same GantMan MobileNetV2 via vendored TF.js
  (`vendor/`, see the extension's `NOTICE`). Verify with
  `npx web-ext lint -s firefox-extension` and
  `node firefox-extension/test/{bayes_parity,rules_check}.mjs`.
- `internal/models/` — `Policy`/`GlobalSettings` structs + JSON schema
  (custom `UnmarshalJSON` per sub-config for defaults + legacy-schema
  migration — see `SafeSearchConfig`'s flat-to-`engines`-map migration as
  the pattern). Policies also carry an `inactive` flag and a `schedule`
  (time-window activation, `internal/models/schedule.go`).
- `internal/proxy/` — the MITM engine (`engine.go`), pipeline
  (`FlowContext`, ordered `[]Addon`), block-page rendering, and the SOCKS5
  listener (`socks5.go` — CONNECT only, no-auth or username/password via
  the shared `ProxyAuthGate`, joins the same tunnel/MITM path as HTTP
  CONNECT). `proxy_listen` entries take `host:port`, `regular@host:port`,
  or `socks5@host:port` forms.
  - `internal/proxy/state/` — `Runtime`: hot-reloaded settings/policies,
    `GetPolicy(clientIP)` tiered MAC→IP→CIDR→catch-all matching (see
    `policy_match.go` for the full decision logic incl. schedules)
  - `internal/proxy/addons/` — all filtering addons, one file each
- `internal/mgmtapi/` — chi router, REST API, embedded UI static serving
- `internal/classify/textbayes/` — embedded pure-Go Bayesian adult-text
  scorer (implements `addons.MLScorer`). The feature table
  (`model_data.json`, `//go:embed`) is regenerated offline by
  `scripts/build_text_bayes_model.go` from local wordlist snapshots; the
  seed vocabulary is curated from LDNOOBW (CC-BY-4.0) — see the package's
  `NOTICE`.
- `internal/classify/image/` — pure-Go NSFW image classifier:
  GantMan/nsfw_model (MobileNetV2, MIT) embedded as `model.bin`
  (`//go:embed`), executed by a from-scratch pure-Go inference engine
  (`nn.go`). See `scripts/nsfw-model/README.md` for provenance/regeneration.
- `internal/logstore/` — SQLite-backed request/block/policy-change logs
- `internal/tun2socks/` — optional TUN-device traffic capture
  (xjasonlyu/tun2socks) that funnels whole-OS traffic into the SOCKS5
  listener; configured via the `tun2socks` block in settings. When it's
  enabled and no SOCKS5 listener is configured, `run` adds one on
  `127.0.0.1:1080`.
- `ui/` — management web UI, copied verbatim from the Python original
- `packaging/` — systemd units, `install.sh`, `.deb` build, Windows-service
  notes (see `packaging/README.md`)

## Known gotchas (don't rediscover these the hard way)

- **Policy selection is by source, first match wins**, tiered
  MAC→exact-IP→CIDR→catch-all. Two modifiers layer on top: policies with
  `inactive: true` or an enabled `schedule` whose time windows don't cover
  "now" are skipped entirely, and **within a tier an actively-scheduled
  policy outranks an unscheduled one** (so a stricter bedtime policy can
  override the regular one for the same client). Schedules fail open —
  disabled or empty-window schedules mean "always active". When testing
  against the live proxy, check `GET /api/policies` to see which named
  policy actually applies to your test client before assuming `default`
  is what's active.
- **Blocked responses return HTTP 200** with a block-page body, not 4xx —
  don't use the HTTP status code alone to tell whether a request was
  filtered. Check `GET /api/logs?kind=requests` (`action`: `ok`/`modified`/
  `blocked`, `component`) or `?kind=blocks` (includes `reason`) instead.
  `?kind=policy_changes` is the policy-edit audit log (always on, not
  gated by any settings toggle).
- **SafeSearch engine matching is host-*and*-path/param scoped, not just
  host-based, for engines whose AI/images/videos tabs live on the *same*
  domain as regular search** (DuckDuckGo, and Google's current `udm=`-based
  unified nav). Blocking "the AI tab" by matching the whole hostname is
  only correct when that tab genuinely lives on a separate domain (Gemini,
  Copilot) — see `internal/proxy/addons/safesearch.go`'s `aiDomains` vs
  `aiPaths`/`aiParams` split and the regression tests for the DuckDuckGo/
  Google bugs this caused in practice.
- **Google shards its image-CDN thumbnail hosts** (`encrypted-tbn0` through
  at least `encrypted-tbn3`.gstatic.com) — matching against a single
  hardcoded hostname silently misses most real traffic. Use prefix/suffix
  matching (`isImageCDNHost`), not an exact-match set, for any CDN-style
  hostname family like this.
- **Response bodies reaching addons are always identity-encoded.** The
  engine strips the client's `Accept-Encoding` before the upstream fetch so
  the stdlib Transport negotiates gzip itself and transparently decodes it
  (see the comment in `internal/proxy/handler.go`'s `handleOneRequest`) —
  don't "fix" that by forwarding the browser's header again, or every
  content-inspecting addon (text_classifier, image_classifier's inline
  scan, youtube_filter) silently starts scanning compressed bytes on
  real-world traffic. `TestProxyDecodesGzipUpstream` guards this.
- **NSFW images aren't only served as `image/*` responses.** Google Images
  inlines its entire initial result grid as base64 `data:image/...` URIs
  inside the search HTML (with JS-string escaping: `\/`, `\x3d`), so the
  browser renders real thumbnails before the separately fetched — and
  filtered — network copies arrive. image_classifier therefore also scans
  HTML/CSS/JS/JSON bodies and rewrites matching data URIs in place — see
  `filterInlineImages` in `internal/proxy/addons/image_classifier.go` and
  its tests before touching the Content-Type gating.
- **Settings changes need a restart; policy changes hot-reload.** Matches
  the Python original — don't expect a `PUT /api/settings` to take effect
  without restarting `webfilter run`.
- **Never unmarshal a *partial* policy body over an existing policy.** Every
  sub-config's `UnmarshalJSON` resets the whole sub-config to defaults
  before overlaying the input, so `{"text_classifier":{"enabled":true}}`
  silently wipes a custom threshold back to 0.80. Merge at the raw-JSON
  level first (`settingsvc.MergePolicyPatch`) or write full documents
  (what `PUT /api/policies/{name}` and `mobile.UpdatePolicyJson` do).
- **The MDM lock lives in `config/managed.json`, not settings.json.** It is
  written only by `settingsvc.ApplyManagedConfig` (Android managed
  configurations) and re-read per request by `mgmtapi`'s `requireUnlocked`
  middleware (403 on settings/policy/cert-import mutations) and the
  `mobile.Update*Json` functions. Desktop never writes it; missing file =
  unlocked. New mutating mgmt routes must take the middleware —
  `TestMutatingRoutesAreLockGated` fails otherwise. The applied restrictions
  doc is hashed so identical re-applies are no-ops (otherwise the
  `mgmt_password` restriction would re-scrypt and rewrite settings.json on
  every app start).
- **Android restriction bundles have no float type.** Thresholds travel as
  string restrictions ("0.8"); the models' `decodeJSONFloat`/`decodeJSONInt`
  already accept string-typed numbers — keep it that way. The restriction
  keys in `android/.../res/xml/app_restrictions.xml` and the preference keys
  in `PreferenceDataStores.kt` are the same identifiers by design; keep the
  two (and `ManagedConfig.buildDocFromBundle`) in sync.
- **Both classifiers are opt-in per policy and need zero setup.**
  `text_classifier.enabled` / `image_classifier.enabled` (both default
  off — NSFW false positives have real cost) are the only switches; there
  is no model directory, download, or sidecar library for either anymore.
  `text_classifier_model_path` in settings is **deprecated and ignored**,
  kept only for backward-compatible JSON round trips. The text addon runs
  a high-precision keyword prefilter (3 hits = block, even on tiny pages)
  and then the embedded Bayesian scorer against the policy's
  `text_classifier.threshold`; the 100-character floor only shields
  weak/ambiguous text from Bayesian scoring noise.
- `http.FileServer`/`FileServerFS` must not be reintroduced for the UI
  static path — it causes a `/` ↔ `/index.html` redirect loop with this
  UI's own navigation. See `internal/mgmtapi/static.go` and
  `TestIndexDoesNotRedirectLoop`.
- Test helpers that construct a `Server`/`CA`/`PolicyStore`/log store
  directly must seed **absolute** temp-dir paths for `cert_dir`/
  `policies_dir`/`logs_dir` — the documented relative defaults (`./certs`
  etc.) resolve against the test process's working directory, not the
  settings file's location.
- WireGuard listen mode is explicitly out of scope: `/api/wireguard` is a
  deliberate 501 stub (`internal/mgmtapi/routes_wireguard.go`) that the
  unmodified UI degrades around gracefully — don't "implement" it or turn
  it into a 404.
- **The UI's Alpine.js and qrcodejs are vendored, not CDN-loaded**
  (`ui/alpine.min.js`, `ui/qrcode.min.js`, referenced with relative `src`) so
  the mgmt UI works offline — required by the Android WebView. Don't
  re-point the `<script>` tags at a CDN; `grep cdn.jsdelivr ui/` must stay
  empty. Provenance/licenses are in `ui/NOTICE`.
- **The Android port is a separate build; `go build ./...` does not exercise
  it.** `mobile/` compiles on any host, but its real target is
  `GOOS=android`. After touching `mobile/` or `internal/app`, run
  `GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build ./mobile` (`ci.yml`'s
  cross-compile matrix also runs this) — the on-device data path
  (VpnService→tun2socks→engine), `modernc.org/sqlite` under the Android
  runtime, and image-CNN latency need a real device/emulator; the APK is
  built by `android.yml` (manual trigger, and automatically on `v*` tags via
  `ci.yml`'s release job, which attaches it to the GitHub release).
- **The x86_64 emulator ABI needs a libc patch or it SIGSYS-crashes on the
  first sqlite open.** `modernc.org/libc`'s musl syscall dispatchers issue
  legacy path-based syscall numbers (lstat #6, open #2, ...) that Android's
  app seccomp policy kills on x86_64 only. Run
  `go run scripts/patch_libc_seccomp.go` before `gomobile bind` when the
  target list includes `android/amd64` (it copies libc to the gitignored
  `third_party/libc-seccomp`, reroutes the dispatchers through a
  `seccompSyscall` shim that remaps to the *at family, and adds a go.mod
  `replace`); `-undo` reverts it — **never commit the replace line**. arm64
  real devices don't need it. `android.yml` runs the patch itself.
- **The Firefox extension is also outside `go test ./...`.** After touching
  `firefox-extension/`, run its lint + Node tests (see the layout entry). If
  you change `internal/classify/textbayes/model_data.json` or the scorer, the
  extension's generated `bayes_model.js`/`bayes_vectors.json` must be
  regenerated or the parity contract silently rots.
- Local `main` may lag GitHub because fixes land through PRs — fetch
  `origin/main` and reconcile before publishing changes.

## When testing against a live running instance

Verify behavior via the mgmt API and request/block logs, not just curl
status codes — see the gotcha above. A useful loop:

```bash
curl -s http://127.0.0.1:8000/api/policies         # which policies exist, and their source_ips
curl -s http://127.0.0.1:8000/api/policies/<name>  # full config for one
curl -s -X PUT http://127.0.0.1:8000/api/policies/<name> -d @updated.json
# ... exercise the proxy at 127.0.0.1:8080 ...
curl -s "http://127.0.0.1:8000/api/logs?kind=requests&limit=20"
curl -s "http://127.0.0.1:8000/api/logs?kind=blocks&limit=20"
curl -s "http://127.0.0.1:8000/api/logs?kind=policy_changes&limit=20"
```

If you temporarily change a live policy for testing, restore it to what you
found before finishing up.

## Related agent docs

[AGENTS.md](AGENTS.md) is the equivalent guidance file for other AI
agents — if you change build requirements, layout, or gotchas here, keep
AGENTS.md in sync.
