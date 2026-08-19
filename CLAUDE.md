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
`gui` (native desktop management window, gogpu/ui — same self-host-or-attach
decision as `tray`; closing the window stops a self-hosted engine but never
an attached one), `service` (Windows service management),
`categories update`, `oui update`, `tun2socks download|status|cleanup`,
`version`.

`config/settings.json` and `policies/*.json` are gitignored runtime state —
first start bootstraps them from `config/settings.example.json` /
`policies/default.json.example`. They persist to disk; the mgmt API's
`PUT /api/policies/{name}` writes straight through to `policies/{name}.json`.
Request/block/audit logs go to SQLite at `logs/webfilter.db`.

## Layout

- `cmd/webfilter/` — cobra CLI; `runners.go` delegates engine construction to
  `internal/app`. The desktop-only tun2socks supervision (`runEngineWithTun`)
  stays here because it is root/`ip`-gated and OS-coupled.
- `cmd/webfilter/internal/gui/` — native desktop management UI
  (github.com/gogpu/ui, pure Go/WebGPU, still CGO_ENABLED=0). Lives under
  `cmd/`, **not** top-level `internal/`, so the Android sweep
  (`GOOS=android go build ./mobile ./internal/...`) never compiles the gogpu
  windowing stack. Sub-packages `mgmtclient/` (typed loopback HTTP client)
  and `uimodel/` (headless form/poll view-models) import no gogpu packages
  and carry the tests; the widget files (`gui.go`, `screen_*.go`) are thin
  over them. Covers dashboard/policies/logs/settings; everything else defers
  to the "Open Web UI" button. No build tags — headless Linux just never
  runs `webfilter gui` (an X11/Wayland display is a runtime need of that one
  command, not a build dependency).
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
  (`Start(dataDir, tunFd)`, `StartProxyOnly(dataDir)` — same bring-up minus
  the TUN, for the no-VPN loopback-proxy/PAC mode — `Stop`, `IsRunning`,
  `Status`, `MgmtUrl`, `ReloadPolicies`, `CaCertPem`, plus JSON-string
  accessors for the native UI and MDM path, one file per concern:
  `settingsapi.go` (`GetSettingsJson`, `UpdateSettingsJson`, `GetPolicyJson`,
  `UpdatePolicyJson`), `managed.go` (`GetManagedStateJson`,
  `ApplyManagedConfigJson`), `policiesapi.go` (`ListPoliciesJson`,
  `CreatePolicyJson`, `DeletePolicy`), `categoriesapi.go`
  (`ListCategoriesJson`, `DownloadCategoryJson`, `DeleteCategory`), and
  `logsapi.go` (`QueryLogsJson`, `AnalyticsJson` — read-only, not
  lock-gated)) and drives the `xjasonlyu/tun2socks` **library** in-process from
  the VpnService `fd://` descriptor — **not** via `internal/tun2socks`'s
  external-binary supervisor (root/`ip`-gated). Android needs no elevation
  because the OS hands the app its TUN fd, which is why the go.mod dependency
  on the library must stay even though desktop no longer links it. The
  TUN-capture file is `tun_capture.go`
  (`//go:build android || linux`), deliberately **not** named `*_android.go`:
  a GOOS filename suffix ANDs with the build tag and would exclude it on a
  Linux desktop, breaking `go test ./mobile`. Build:
  `gomobile bind -target=android/arm64,android/arm -androidapi 26 -o android/app/libs/webfilter.aar ./mobile`.
- `android/` — Kotlin/Gradle app scaffold (VpnService with a proxy-only
  no-TUN mode, WebView mgmt UI, native settings screens backed by the
  `mobile/` JSON API, native multi-policy manager / category-blocklist /
  logs / analytics screens, per-app filtering with app icons, MDM managed
  configurations via `app_restrictions.xml` + `ManagedConfig.kt`, CA
  install/save flow). See `android/README.md` for local
  build steps and the TestDPC verification recipe;
  the AAR is a build artifact (gitignored). The debug APK can also be built on
  demand by the **manual** `.github/workflows/android.yml` workflow
  (`workflow_dispatch` only — Actions tab → "Android APK" → Run workflow;
  artifacts: APK + AAR).
- `internal/settingsvc/` — settings/policy merge + validation shared by
  `PUT /api/settings` and the `mobile/` native path (they must behave
  byte-identically), plus the managed-config apply logic
  (`ApplyManagedConfig`).
- `internal/models/` — `Policy`/`GlobalSettings` structs + JSON schema
  (custom `UnmarshalJSON` per sub-config for defaults + legacy-schema
  migration — see `SafeSearchConfig`'s flat-to-`engines`-map migration as
  the pattern). Policies also carry an `inactive` flag and a `schedule`
  (time-window activation, `internal/models/schedule.go`).
- `internal/proxy/` — the MITM engine (`engine.go`), pipeline
  (`FlowContext`, ordered `[]Addon`), block-page rendering, and the SOCKS5
  listener (`socks5.go` — CONNECT only, no-auth or username/password via
  the shared `ProxyAuthGate`, joins the same tunnel/MITM path as HTTP
  CONNECT), plus a SOCKS4/4a listener (`socks4.go` — CONNECT only, no auth
  channel). `proxy_listen` entries take `host:port`, `regular@host:port`,
  `socks4@host:port`, or `socks5@host:port` forms, each optionally
  TLS-wrapped: `https@` (HTTP proxy over TLS), `tls@` (SOCKS5 over TLS), or
  the general `tls+<base>@` prefix. `models.ParseListenSpec` returns the base
  mode + TLS flag; `Engine.dispatchConn` terminates TLS with a CA-minted
  endpoint leaf, then dispatches by base mode.
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
- `internal/tun2socks/` — optional TUN-device traffic capture, configured via
  the `tun2socks` block in settings. It **supervises the official tun2socks
  binary as a child process** (`supervisor.go`) rather than linking the
  library, so the filtering engine needs no elevation of its own beyond the
  capability to create the TUN device and set routes (see the systemd gotcha
  below). `download.go` installs it from the upstream GitHub release
  (sha256-verified) into `bin/` beside the webfilter executable; `binary.go`
  resolves it (installed copy first, then PATH). Reachable from
  `POST /api/tun2socks/download` and `webfilter tun2socks download|status`.
- `ui/` — management web UI, copied verbatim from the Python original
- `packaging/` — systemd units, `install.sh`, `.deb` build, Windows-service
  notes (see `packaging/README.md`)

## Known gotchas (don't rediscover these the hard way)

- **tun2socks is an external process, and its SOCKS5 listener is engine-owned.**
  `internal/tun2socks` execs the downloaded binary; it does **not** link the
  library (only `mobile/` still does — see the layout note). Capture is fed by
  a dedicated `socks5@127.0.0.1:0` listener registered through
  `Engine.InternalListen` (`app.EnsureTunSocksListener`), **not** a
  `proxy_listen` entry: it never appears in settings.json or the UI's listener
  editor and cannot be retargeted. Port 0 means the OS assigns it, so read the
  real address back with `proxy.FindPurpose(listeners, …)` after `Listen()` —
  which is why the supervisor must start *after* listeners are bound. The old
  free-text `tun2socks.proxy_target` setting is gone; an old settings.json
  still loads because encoding/json ignores the unknown key.
- **A missing tun2socks binary, or no root *and* no CAP_NET_ADMIN, must stay a
  `StartupSkippedError`.** TUN capture is an add-on: `runEngineWithTun` logs
  the skip and keeps serving the proxy. Only genuine wiring bugs (e.g. no SOCKS
  address) return a hard error that takes the process down.
- **The Linux privilege gate is root *or* CAP_NET_ADMIN, not euid 0.**
  `platform_linux.go`'s `hasRoutePrivileges` probes the effective capability
  set with `unix.Capget`, because the shipped systemd units run as the
  unprivileged `webfilter` user and get `AmbientCapabilities=CAP_NET_ADMIN
  CAP_NET_RAW` from the `packaging/tun2socks.conf` drop-in (installed by
  `install.sh` only when settings enable tun2socks). Two non-obvious facts
  behind that design: **ambient** capabilities survive `execve`, so the `ip`
  calls and the tun2socks child inherit them, whereas **file** capabilities
  (`setcap`) are dead on arrival because the units set `NoNewPrivileges=true`;
  and `CAP_NET_RAW` is needed too, because tun2socks calls `SO_BINDTODEVICE`
  whenever `tun2socks.interface_name` is set. Keep `describeRoutePrivilege` a
  pure function — it is what makes the gate testable in an unprivileged
  `go test`.
- **Capture uses policy routing; it never writes to the main routing table.**
  `linuxroutes.go` puts the default route in private table `8888` and selects
  it with `ip rule` pref 9100, with pref 9000 (`fwmark 0x5745 -> main`)
  exempting the engine's own traffic and `throw` routes in table 8888 applying
  `bypass_cidrs`. The first implementation did `ip route replace default ...
  metric 1` in **main**, which had two live-host failure modes that no unit
  test could see: the engine's own upstream fetches followed the new default
  back into the TUN and looped (engine -> tun2socks -> engine), and nothing
  ever restored the displaced route, so stopping the service left the box
  offline. Do not "simplify" this back into the main table.
- **The engine marks its own upstream sockets (`SO_MARK 0x5745`) and every
  outbound dial must go through `internal/proxy/upstream.go`.** A new
  `net.Dial`/`net.DialTimeout`/`http.Transport` anywhere on the egress path is
  a capture loop waiting to happen — use `DialUpstream*`,
  `UpstreamDialer(timeout)`, or `ListenUpstreamPacket`. That includes name
  resolution, which is why `SetUpstreamEgressMark` swaps in a `PreferGo`
  resolver that dials through the same `Control` hook. The mark is process-wide
  and stays 0 unless capture actually starts.
- **`linuxroutes.go` deliberately has no build tag, and must not be renamed to
  `route_linux.go`.** A `_linux` filename suffix is an implicit GOOS
  constraint; the file only builds `ip` argument lists, and keeping it
  buildable everywhere is what lets the riskiest commands in the repo be
  pinned argument-for-argument by tests that run in ordinary CI
  (`linuxroutes_test.go`). The dispatch in `commands.go` still gates on
  `runtime.GOOS`.
- **Teardown has exactly one implementation** (`unconfigurePlatform`), reached
  from the supervisor's shutdown path, from `configureLinux`'s pre-clean, and
  from `webfilter tun2socks cleanup` (which `packaging/tun2socks.conf` also
  runs as `ExecStopPost`). It is best-effort and silent by design: on shutdown
  every command is expected to fail once the state is already gone. The
  pre-clean matters because `ip rule add` is additive — without it each
  restart stacks another copy of the rules.
- **`tun2socks.dns_servers` is Windows-only.** Linux ignores it: captured DNS
  is answered by the policy's DoH resolver in the SOCKS5 UDP relay, so a
  resolver set on the device would never be consulted. `tun_netmask` *is*
  honoured on both now (it used to be hardcoded `/15` on Linux), and
  `bypass_cidrs` is applied on Linux (it used to be validated and then ignored
  on every platform).
- **IPv6 is not captured.** The TUN device is IPv4-only, so a host with an IPv6
  default route reaches dual-stack destinations unfiltered. Capture logs a
  warning when it finds one at start; it does not try to fix it.
- **The SOCKS5 UDP relay drops UDP/443 and UDP/853, and resolves DNS on any
  port.** QUIC (443) and DoQ (853) are dropped unconditionally so neither
  HTTP/3 nor an encrypted resolver can tunnel past the pipeline; that drop is
  **not** gated on `url_filter.block_quic`, which only strips Alt-Svc and can
  be turned off. Port 53 — *and any datagram that strictly parses as a DNS
  query, whatever port it went to* — is answered through the policy's DoH
  filter, because "the resolver lives on 53" is a convention a client can
  simply ignore. The rules live in `udpVerdictFor`/`looksLikeDNSQuery`; keep
  the sniff strict (QR/AA/TC/Z/RCODE clear, one question, no answer records)
  or ordinary UDP protocols start getting rerouted. End-to-end tests can't tell
  a dropped datagram from one sent to a closed port, so assert on those
  functions.
- **A tunnel can be refused before it is spliced, and that path is host-only.**
  `handleTunnel` runs a connection-level gate before the MITM/blind-splice
  decision: `HostFilterVerdict` (`internal/proxy/hostgate.go`) applies the
  policy's host-scoped url_filter rules to blind-spliced hosts — the only
  filtering they can ever get, since no addon runs on a splice — and TCP/853
  (DoT) is refused when the policy filters DNS. Three things to keep straight:
  allow/block patterns containing `/` are **skipped** there (a hostname can't
  decide a path pattern), MITM'd hosts are deliberately left to the `UrlFilter`
  addon so they still get the styled block page, and a refusal is signalled in
  the client's own protocol (HTTP **403**, SOCKS5 `0x02`, SOCKS4 91) via
  `ErrBlockedByPolicy` — it produces a `?kind=blocks` row but **no**
  `?kind=requests` row, because no `FlowContext` exists yet. The category half
  of the verdict is shared with the addon (`proxy.CategoryVerdict`) so the two
  can't drift.
- **Image decoders are registered in two places.** `internal/classify/image`
  (the detector) and `internal/proxy/addons/image_classifier.go` (dimension
  checks + replacement rendering) each need the blank import, and inline data
  URIs additionally need the format listed in `inlineImageRe`. Miss any one and
  the format silently passes unfiltered: an undecodable image makes `Score`
  return `ok=false`, which reads as "not NSFW". JPEG/PNG/GIF/WebP today;
  AVIF and animated WebP still fail open. Test fixtures come from
  `internal/webptest` (a minimal VP8L encoder — Go has no WebP encoder).
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
  filtered. (The one exception is the connection-level gate above, which has
  no response to rewrite and so refuses the tunnel outright.) Check `GET /api/logs?kind=requests` (`action`: `ok`/`modified`/
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
  two (and `ManagedConfig.buildDocFromBundle`) in sync. Two documented
  exceptions: `proxy_only_mode` is consumed by the Kotlin layer only (it
  selects how the service starts, not engine config), and
  `url_filter_categories` is edited natively by `CategoriesActivity`
  rather than a preference widget (the restriction mapping is unchanged).
- **The PAC file advertises the first `regular` listener**
  (`PrimaryRegularProxyPort`, 8080 fallback) — never a SOCKS port; PAC's
  `PROXY` directive is HTTP-only. Proxy-only mode (`mobile.StartProxyOnly` /
  the app's no-VPN switch) injects a session-only `regular@127.0.0.1:8080`
  via `app.EnsureLocalHTTPProxyListener` when settings configure no regular
  listener; the entry is never persisted to settings.json.
- **Category lists may be gzip-compressed on disk.** The store prefers
  `<name>/domains.gz` over the plain `domains` file; per-category downloads
  (`categories.DownloadCategory`, from
  `https://dbl.ipfire.org/lists/<name>/domains.txt`) write `.gz`, while the
  desktop tarball path (`webfilter categories update`) still writes plain
  files. Sets above 100k domains are held as sorted 64-bit FNV-1a hashes,
  not string maps (~8 MB vs ~100 MB for the porn list); a hash collision
  can only over-block, never bypass.
- **Read the log DB from exports via `logstore.NewReader`, never a second
  `logstore.Configure`.** Configure opens a competing write connection and
  runs schema+prune against the engine's single-writer design; Reader is
  the write-free view (fresh read-only conns, fail-open on a missing DB)
  that `mobile.QueryLogsJson`/`AnalyticsJson` use.
- **The "default" policy is protected on the mobile path**: `DeletePolicy`
  and a rename through `UpdatePolicyJson` are refused in Go (it is the
  always-on fallback and the fixed target of the MDM `policy_json`
  restriction). `CreatePolicyJson` creates policies `inactive:true` unless
  the body says otherwise — an ACTIVE schedule-less catch-all would compete
  with `default` by filename sort (see the policy-selection gotcha above).
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
- **The native desktop GUI is an HTTP client of the mgmt API even when it
  self-hosts the engine in-process.** All reads/writes go through
  `cmd/webfilter/internal/gui/mgmtclient` to loopback HTTP — never directly
  to disk or `logstore` — so the MDM lock, audit log, validation, and policy
  hot-reload apply exactly once, server-side. Don't "optimize" it into a
  third direct-write path (after mgmtapi and mobile/), and don't open a
  second SQLite handle for its log viewer. Self-host auth uses
  `mgmtapi.Server.SessionCookie()` (deterministic token, invalidated by a
  password change); the supervisor re-seeds it after every engine restart.
- **gogpu/ui is pinned at v0.x and its API churns between minors.** Treat
  `github.com/gogpu/{ui,gogpu,gg}` upgrades as deliberate changes: re-verify
  the widget option names against the module cache (several differ from the
  docs' examples — e.g. `primitives.CrossAxisCenter`, `textfield.TypePassword`)
  and re-run `webfilter gui` manually. The widget layer is deliberately thin
  over `uimodel`/`mgmtclient` so an API-churn rewrite doesn't touch tested
  logic.
- **The GUI drives its own render loop (`gui.runRenderLoop`), NOT
  `desktop.Run`.** gogpu/ui v0.1.44's `desktop.Run` compositor mis-behaves for
  this UI: it double-applies the DPI scale (2.25× and cropped on a 150%
  display) and its per-boundary GPU textures never clear, so a previous tab's
  content bleeds through on tab switch. Our loop clears the whole gg canvas
  every frame and applies the DPI scale exactly once (`cc.Scale(scale)` on a
  physical-pixel canvas created with `ggcanvas.NewWithScale(provider, physW,
  physH, 1.0)` — gg's own `WithDeviceScale>1` scales twice in v0.50.5). This
  is the same stateless full-repaint the `offscreen` snapshot renderer uses,
  which is why those snapshots are always correct.
- **Only call `Window().HandleResize` when the size actually changed.** It
  unconditionally sets needsLayout/needsRedraw/needsFullRepaint and marks the
  whole tree dirty; calling it every frame (to keep the ui window's size in
  sync, since gogpu never delivers resizes to the EventSource the ui window
  subscribes to) keeps gogpu/ui's 30fps animation pumper alive forever and
  pegs a CPU core on an idle window. Gate it on `w != lastW || h != lastH`.
- **The lists use `gui.scrollBox`, not `core/listview` or `core/scrollview`.**
  In v0.1.44 `listview` is a virtualized repaint boundary that renders blank
  (or bleeds a stale texture) in the direct-DrawTo loop, and `core/scrollview`
  self-invalidates every frame once its content overflows (~100fps, CPU-pegged
  idle window). `scrollBox` is a plain clip+translate container that only
  requests a frame on an actual wheel event. Lists are a `VBox` of ordinary
  row widgets inside it, capped at `maxListRows`; the full history is behind
  "Open Web UI".
- **The GUI's gg GPU accelerator is registered by `cmd/webfilter/cmd_gui.go`
  (`_ "github.com/gogpu/gg/gpu"`), NOT by the `gui` package.** That blank
  import swaps gg's software rasterizer for the GPU one process-wide; if the
  `gui` package imported it, the package's own `offscreen` snapshot test
  (`snapshot_test.go`, `GUI_SNAPSHOT_DIR=<dir> go test ./cmd/webfilter/internal/gui`)
  would render blank. Keep GPU registration in the command, CPU-only in the
  package.
- **After async data lands, the GUI's `redraw()` marks the root
  needs-layout, then requests a frame.** gogpu/ui's demand-driven `Frame()`
  only re-lays-out when `needsLayout` is set, and swapping a list/editor's
  content (via `swapWidget.SetChild`) or a signal change doesn't set it at the
  root — so the new content would draw with a stale/empty layout. `onTabSelected`
  also calls `redraw()` so a switched-to tab lays out the same frame.
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
