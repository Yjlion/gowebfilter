# TODO

The feature roadmap. Every open item names why it matters and which files it
touches, so a session can pick one up without re-deriving the context.

Size tags: `S` a day or less, `M` a focused session, `L` multi-session or needs
hardware/an OS not available in this environment.

Before starting anything here, read [CLAUDE.md](CLAUDE.md)'s gotchas — several
items below are one gotcha away from being implemented wrong.

## 0. Security: default-install takeover

These outrank even the coverage gaps: each one lets someone other than the
admin turn the filter off, not just slip past it.

- [ ] **The mgmt API is open to every proxied client and every website by default.** `S`
  `config/settings.example.json` ships `auth_enabled: false` with an empty
  `password_hash`, and `authMiddleware` passes every request when either is
  unset (`internal/mgmtapi/middleware.go:47`). `ManagementAccess` lets a LAN
  client reach the UI at `web.filter`, so a filtered client can edit its own
  policy. Separately, nothing checks Origin, Host or Content-Type
  (`readJSON`, `internal/mgmtapi/jsonutil.go`), so any web page can send a
  preflight-free PUT to `127.0.0.1:8000`, and DNS rebinding can read
  `GET /api/certs/export`, which includes the CA private key. Fix: require a
  password on first run (or refuse mutations and mgmt passthrough while auth
  is off), reject non-JSON bodies, and check Host/Origin.

- [ ] **Android: any installed app can reconfigure the filter.** `M`
  `mobile/settings.go:18` claims the loopback mgmt server "is only reachable
  from the app's own WebView". Any app with INTERNET permission can reach
  `127.0.0.1`, though, and auth is off by default, so any app can
  `PUT /api/policies/default`. `requireUnlocked` only helps under an MDM lock.
  Fix: a per-launch secret that only the WebView (and the native screens,
  which go through the gomobile API anyway) receives.

- [ ] **Session and login hardening.** `S`–`M`
  The session token is a fixed HMAC of the password hash (`sessionToken`,
  `internal/mgmtapi/auth.go:27`): the 7-day expiry is enforced only by the
  browser, logout doesn't revoke it, and a stolen cookie works until the
  password changes. `handleLogin` (`auth.go:71`) has no rate limit, lockout, or
  failure logging. `new_password` is accepted without the current password
  (`internal/settingsvc/settingsvc.go:66`). The cookie never sets `Secure`,
  there are no CSP / `X-Frame-Options` / `nosniff` headers, and the mgmt
  `http.Server` has no timeouts (`internal/app/engine.go:155`).

- [ ] **Data race on CA import.** `S`
  `handleCertsImport` assigns `s.CA = newCA`
  (`internal/mgmtapi/routes_certs.go:70`) with no lock while the download and
  export handlers read `s.CA` concurrently. `go test -race` would flag it once
  a test covers import alongside a download.

## 1. Filtering coverage gaps

Places traffic escapes the pipeline **today**. These outrank everything below:
each one is a way the filter silently does nothing. Verified against the code,
not speculative.

- [ ] **Transparent mode: a forged SNI buys a blind splice to any IP.** `M`
  `transparent.go:52` takes `hostOnly` from the peeked SNI/Host header, and
  `handleTunnel` makes the MITM-exclusion and host-gate decisions on that
  name but then splices to the recovered `origDst`
  (`internal/proxy/handler.go:210`), which is never checked against the name.
  A client that sends SNI `chase.com` (MITM-excluded in
  `policies/default.json.example`) gets an unfiltered tunnel to any IP:443:
  a VPN, a proxy, or a blocked site reached by address. Explicit CONNECT and
  SOCKS aren't affected, because there the upstream is dialled *by* the name.
  Fix: in transparent mode, splice only if `origDst` is in the name's
  resolution; otherwise MITM or refuse.

- [ ] **Host and URL matching aren't normalised.** `S`
  `HostMatches`/`fnmatch` (`internal/proxy/matching.go`) are case-sensitive
  and don't strip a trailing dot, so `blocked.com.` gets past a `blocked.com`
  or `*.blocked.com` rule in the custom lists and in `HostFilterVerdict`. The
  same exact-match miss hits `youtubeHosts` and the SafeSearch `domains` sets.
  Categories already normalise. Path patterns match `URL.String()`
  (`internal/proxy/addons/url_filter.go:26`), which keeps the client's
  `RawPath`, so `/%62ad` gets past a `/bad` rule while the server serves
  `/bad`. Normalise once (lowercase, trim the dot, decode the path) before any
  matcher sees the host or path.

- [ ] **SafeSearch coverage holes.** `S`–`M`
  In `internal/proxy/addons/safesearch.go`, Bing's `adlt=strict` and Yandex's
  `fyandex=1` are only injected under `pathPrefix: "/search"`, so
  `/images/search` and `/videos/search` get no SafeSearch unless the tab is
  blocked outright (Brave is covered by its cookie). DuckDuckGo is an exact
  domain set, so `html.duckduckgo.com` and `lite.duckduckgo.com` escape.
  Ecosia, Startpage and Qwant aren't listed. YouTube Restricted Mode keys on
  `.youtube.com` and misses `youtubei.googleapis.com` and
  `www.youtube-nocookie.com`, both on Google's own enforcement list.

- [ ] **YouTube channel filter coverage.** `M`
  `youtubeHosts` (`internal/proxy/addons/youtube_filter.go:26`) lacks
  `music.youtube.com` and `www.youtube-nocookie.com`, so embeds are
  unfiltered. Only four exact `youtubei` API paths are handled: Shorts
  (`/youtubei/v1/reel/…`, `/shorts/`) and `/embed/` fall through, and the
  JSON-only decode means the mobile apps' protobuf responses pass.

- [ ] **The image classifier trusts the server's Content-Type.** `S`
  `image_classifier.go:175` only scores responses whose header starts with
  `image/`, case-sensitively. `application/octet-stream` (common on S3), a
  missing type, or `Image/JPEG` are skipped, yet browsers still render them in
  `<img>`. Sniff the bytes (`http.DetectContentType`) when the header isn't
  a known non-image type.

- [ ] **Gateway mode: IPv6 and other ports go around the filter.** `M`
  `internal/gateway/ruleset.go` only builds a `table ip`, so a gateway that
  also forwards IPv6 (e.g. it sends router advertisements) passes all IPv6 web
  traffic untouched. Only `intercept_ports` are redirected and only UDP
  443/853 is dropped, so TCP/853 (DoT) and HTTPS on other ports are
  forwarded unfiltered. At minimum, fail closed: drop forwarded IPv6 and
  TCP/853.

- [ ] **The Android VPN routes no IPv6.** `S`
  `WebFilterVpnService.kt:71-73` adds only an IPv4 address and
  `0.0.0.0/0`, so on dual-stack networks IPv6 bypasses the TUN. Nothing
  documents this for Android (the CLAUDE.md IPv6 note is about desktop TUN).
  Cheap fix: add an IPv6 address and a `::/0` route, and drop that traffic.

- [ ] **User-Agent exclusion also skips the URL block lists.** `S` (decide first)
  `mitm_control.go` sets `MitmPassthrough` from the client-supplied
  User-Agent, and `UrlFilter` deliberately honours it. With
  `ua_mode: exclude` and, say, `okhttp`, a browser that sets that UA escapes
  even the explicit block list. Decide whether block lists and categories
  should still apply to UA-excluded flows (the host gate already applies them
  to spliced ones).

- [ ] **AVIF and animated WebP fail open.** `M`
  An image with no registered decoder makes `Score` return `ok=false`, which
  the addon reads as "not NSFW" — so an NSFW AVIF passes unfiltered. Adding a
  format means all three registration points, or it silently stays broken:
  `internal/classify/image`, `internal/proxy/addons/image_classifier.go`, and
  `inlineImageRe` for inline data URIs. Test fixtures come from
  `internal/webptest` (Go has no WebP encoder).

## 2. Parental-control UX

- [ ] **"Why was this blocked?" endpoint and richer block page.** `S`
  The blocks table already stores `reason`/`component`/`policy`/`url`; the
  block page shows only the reason and filter name. Surface a lookup endpoint
  and expand the template so a user can see which rule fired.
  Touches: `internal/proxy/block_template.html`, `internal/mgmtapi/routes_logs.go`.

- [ ] **Per-client live activity dashboard.** `M`
  `logstore.Analytics` already returns `PerDevice []DeviceStats`
  (`internal/logstore/read.go:100`); what's missing is a per-client drill-down
  and a live/polling view.
  Touches: `internal/mgmtapi/routes_analytics.go`, `ui/analytics.html`.

- [ ] **Schedule presets: school hours, bedtime, weekends, temporary unlock.** `S`
  Pure UI over the existing `internal/models/schedule.go` — the evaluation
  logic (including overnight windows and within-tier scheduled-beats-unscheduled
  precedence) is already done and tested.
  Touches: `ui/policy-editor.html`.

- [ ] **Deeper SafeSearch/YouTube simulation.** `S`
  The Policy Simulator's addon hints are `would_modify`-level rather than an
  actual rewritten URL, so you can't tell *what* SafeSearch would do to a
  query. Touches: `internal/mgmtapi/routes_policy_simulator.go`.

- [ ] **DNS/category cache visibility in the management UI.** `S`
  Which category lists are loaded, their domain counts, whether a set is held
  as strings or as the hashed representation (>100k domains), and when it was
  last updated. Touches: `internal/categories/store.go`,
  `internal/mgmtapi/routes_categories.go`.

- [ ] **Alerts / webhook on repeated blocks.** `M`
  Today a parent has to go read a log. A per-policy webhook (or email) fired on
  N blocks from one client in a window is the signal they actually want.
  Hook off `internal/proxy/addons/request_logger.go`; keep the send
  off the request path.

- [ ] **Temporary allow/deny and request-access from the block page.** `L`
  A "request access" button on the block page, an admin approve/deny action,
  and a TTL'd allow entry that survives policy hot-reload but not a restart.
  Depends on the "why blocked" item for the request payload.
  Touches: `internal/proxy/block.go`, a new `internal/mgmtapi` route (must take
  `s.requireUnlocked`), and a TTL store in `internal/proxy/state`.

- [ ] **Time/quota budgets per policy.** `L`
  "One hour of YouTube a day" — a new policy sub-config plus a per-client
  counter. Two constraints: a new sub-config needs a defaults-resetting
  `UnmarshalJSON` like `SafeSearchConfig`, and the counter needs somewhere to
  live that survives policy hot-reload.

## 3. Ops and platform

- [ ] **Backup/restore of config + policies.** `M`
  `internal/mgmtapi/routes_backup.go` is a 10-line empty stub whose own comment
  states the requirement: the restore route must be wrapped in
  `s.requireUnlocked` or `TestMutatingRoutesAreLockGated` fails. Restore must
  write full policy documents — a partial body gets reset to defaults by the
  sub-config unmarshalers (`settingsvc.MergePolicyPatch` is the alternative).
  Pair it with a `webfilter config validate` subcommand over the same
  `internal/settingsvc` validation, so a bundle can be checked before import
  and a hand-edited `policies/*.json` before a restart.

- [x] **Container image.** `S`
  `Dockerfile` + `docker-compose.yml` at the repo root, documented in
  [docs/docker.md](docs/docker.md). Static binary on Alpine, non-root, one
  `/data` volume holding `config/`, `policies/`, `certs/`, `categories/` and
  `logs/`. No settings file is baked in: `config.BootstrapRuntimeFiles`
  generates one with `0.0.0.0` binds and absolute `/data` paths on first
  start, which `settings.example.json` (loopback binds, CWD-relative dirs)
  would not. Built (not published) by the `docker` job in `ci.yml`.

- [x] **`/metrics` and `/health` endpoints.** `S`
  `internal/metrics` is a dependency-free Prometheus text-exposition registry
  (counters/gauges/histograms); `internal/mgmtapi/routes_ops.go` serves
  `/metrics` and a cheap unauthenticated `/health`. Instrumented at the
  existing choke points: `RequestLogger.record`, `FlowContext.LogBlock` plus
  the two connection-level block sites, both classifiers, the upstream
  round-trip, and `dispatchConn`. Scrapers authenticate with the optional
  `metrics_token` bearer, since a scraper cannot hold a session cookie.
  Documented in [docs/metrics.md](docs/metrics.md), including that counters
  are per-process and a standalone `mgmt` process reports zeroes.

- [ ] **Category management from the desktop mgmt API.** `M`
  `internal/mgmtapi/routes_categories.go` registers exactly one route,
  `GET /api/categories` — there is no download or delete. So the web UI can
  list category blocklists but cannot install or refresh one; the only ways in
  are the `webfilter categories update` CLI (whole tarball) and the Android
  path (`mobile/categoriesapi.go` → `categories.DownloadCategory`, per
  category). Expose the per-category download/delete the mobile API already
  has, then add the auto-update ticker on top — there is no scheduler in
  `internal/categories` or `internal/app`, so lists go stale silently.
  New mutating routes must take `s.requireUnlocked`.

- [ ] **API tokens for automation.** `M`
  Auth is a single session cookie derived from the password hash
  (`sessionToken`, `internal/mgmtapi/auth.go:27`), so any script must log in as
  the admin and breaks on every password change. Revocable, optionally
  read-only tokens would fix both.

- [x] **HTTPS for the management UI.** `M`
  `mgmt_tls` serves the UI and API (including the login POST) over TLS.
  Certificate comes from `mgmt_cert_file`/`mgmt_key_file` when set, and is
  otherwise minted by the runtime CA through `certs.ServerTLSConfig` — the
  SNI-less fallback rule is now shared with `Engine.proxyTLSConfig` instead
  of duplicated. Android forces plaintext (`Server.ForcePlaintext`): the
  WebView has no trust path to a CA-minted leaf, and an MDM push could
  otherwise lock an admin out. Documented caveat: with a CA-minted
  certificate, `/api/ca-cert` and `/proxy.pac` sit behind a warning the CA
  itself would resolve, so install the CA out of band or use a real
  certificate.

- [x] **Settings hot-reload.** `L`
  `settingsvc`'s `hotFields` allowlist classifies every field (unknown =
  restart-required, enforced by a test); `Runtime.ApplySettings` swaps the
  hot ones via `MergeHot`, which keeps restart-required fields at the value
  actually in effect so the live snapshot never contradicts what is bound.
  Delivered both in-process (`mgmtapi.Server.OnSettingsSaved`) and by an
  fsnotify watch on the settings directory, which is what covers standalone
  `proxy` + `mgmt`. `PUT /api/settings` returns `restart_required: [...]`
  and both UIs render it. Listener rebind, logstore reopen and
  tun2socks/gateway reconfigure stay out of scope by design.

- [ ] **macOS support.** `L (unverifiable here)`
  Absent from the target list in `scripts/package-release.sh:31-33` and from
  `ci.yml`'s cross-compile matrix, and `internal/tun2socks` has no
  `platform_darwin.go` for route/DNS setup (see `packaging/README.md`).

- [ ] **Android service robustness.** `S`
  There's no `RECEIVE_BOOT_COMPLETED` receiver and no always-on/lockdown VPN
  guidance, so after a reboot the filter stays off until the app is opened. A
  `START_STICKY` restart with a null intent falls through to VPN mode
  (`WebFilterVpnService.kt:44-53`) and ignores `Prefs.proxyOnlyMode`, so
  proxy-only users silently lose filtering. `android:allowBackup="true"` with
  no backup/extraction rules puts the CA key, password hash and
  `managed.json` in cloud backups, and restoring one can undo an MDM lock.

- [ ] **Android tamper resistance.** `M`–`L`
  No device-admin receiver and no uninstall protection. `onRevoke` only logs
  and stops, so revoking the VPN raises no alert. Pairs with the
  webhook-alerts item above.

- [ ] **Audit settings changes, not just policies.** `S`–`M`
  Only policy edits reach `policy_changes` (`routes_policies.go`). Turning
  auth off, changing the password, editing listeners, or importing a CA leaves
  no record.

- [ ] **Bound the log database.** `S`
  `Prune` (`internal/logstore/prune.go`) never touches `policy_changes`, only
  runs every 500 inserts (`write.go:88`, so nothing is pruned while the proxy
  is idle), and there's no `auto_vacuum`/VACUUM or size cap, so the file
  never shrinks.

- [ ] **Windows service hardening.** `S`–`M` (partly unverifiable here)
  `service_windows.go` installs no recovery actions, so the service stays
  down after a crash. It runs as LocalSystem, slog output probably goes
  nowhere under the SCM, and relative paths from a hand-written settings.json
  may resolve under `System32`. Windows TUN capture is still unverified on
  hardware (HANDOFF.md).

- [ ] **CI depth.** `S`
  `ci.yml` runs tests without `-race` and has no staticcheck/govulncheck or
  `gofmt -l` gate. Windows is cross-compiled but never tested. The release
  job attaches a debug-signed APK.

- [ ] **Mobile API and desktop GUI parity.** `M`
  The gomobile API has no policy simulator, classifier health, cert
  export/import, or log export (the WebView covers them). The native GUI shows
  schedules read-only (`uimodel/policyform.go:53`) and can't edit the
  SafeSearch engine list.

## 4. Classifier quality

- [ ] **Measure image-classifier latency and accuracy.** `M`
  HANDOFF.md lists on-device CNN latency as unverified, and the default
  threshold is a guess. A benchmark plus a small labeled fixture set would make
  threshold tuning evidence-based. Cheapest item here and it de-risks the other
  two.

- [ ] **Train the text Bayesian model on real labeled corpora.** `L`
  `scripts/build_text_bayes_model.go` currently assigns fixed adult/safe
  pseudo-counts per seed phrase — the model is a weighted keyword list wearing
  a Bayes hat. Mind the licensing constraints recorded in
  `internal/classify/textbayes/NOTICE` (e2guardian and Redwood are references
  only, not embeddable data).

- [ ] **Non-English adult-text coverage.** `L`
  Only the English LDNOOBW-derived seed vocabulary is embedded, so
  `text_classifier` is near-blind outside English. Same licensing constraint as
  above.

## 5. Docs, i18n and tests

- [ ] **Finish the UI translations.** `M`
  About 100 English keys in `ui/i18n.js`, including the whole Tools page
  (`nav.tools`, `tools.*`), `th.userAgent` and `th.previousName`, are missing
  from all six other languages. They fall back to English, which looks worst
  in the RTL locales. A few keys the UI uses aren't defined even in English
  (`ed.scheduleDay`, `ed.dohCustom`, `ed.minDimension`, `ed.minDimHelp`).

- [ ] **Doc drift.** `S`
  `android/README.md` (around line 204) still says the Kotlin sources have
  never been compiled, which contradicts HANDOFF.md and `android.yml`.
  AGENTS.md lacks CLAUDE.md's gateway-sysctl gotcha, and the two gotcha
  lists should be diffed again for other drift.

- [ ] **Test the capability gate.** `S`
  `internal/netpriv` (the root-or-`CAP_NET_ADMIN` probe TUN capture relies on)
  has no tests. `cmd/webfilter` has none either.

## Done

- [x] **ICAP adaptation service** (`internal/icap` + `internal/proxy/icap.go`)
  — `icap@host:port`/`icaps@` is a served `proxy_listen` mode, so a site that
  already runs Squid can adopt this filtering without replacing its proxy. The
  same pipeline, policies and logs apply; `X-Client-IP` carries the real client
  so per-client tiers survive the hop. Verified against Squid 7.7 in forward,
  `ssl_bump`, peek-and-splice and transparent-intercept modes — see
  [docs/icap.md](docs/icap.md) and HANDOFF.md for what was and was not tested.
- [x] Host-level URL/category filtering for blind-spliced tunnels
  (`proxy.HostFilterVerdict`, `internal/proxy/hostgate.go`) — a MITM-excluded
  host is now checked against the policy's host-scoped allow/block patterns and
  category sets before the splice, and a blocked tunnel is refused at the
  protocol level (HTTP 403 / SOCKS5 0x02 / SOCKS4 91) with a blocks-log row.
  Path patterns are deliberately not applied: a hostname can't decide them.
- [x] DNS-over-QUIC and alternate-port DNS in the SOCKS5 UDP relay — UDP/853 is
  dropped like QUIC, and any datagram that strictly parses as a DNS query is
  resolved through the policy-aware resolver whatever port it was sent to
  (`udpVerdictFor`/`looksLikeDNSQuery`).
- [x] DNS-over-TLS (TCP/853) — refused when the policy enables DoH filtering,
  blind-spliced otherwise; either way it no longer reaches the MITM path's
  HTTP parser.
- [x] `block_quic` defaults on, and is exposed in the web policy editor (it was
  desktop-GUI/Android-only) with help text that says what it does and does not
  do.
- [x] Policy test/simulator API (`POST /api/tools/policy-simulate`) — policy
  selection, schedule status, URL allow/block/category results, addon hints;
  surfaced as the Policy Simulator card on the Tools page.
- [x] Classifier health checks (`GET /api/tools/classifier-health`) — text ML
  readiness vs keyword-only fallback, embedded image classifier availability.
- [x] Scheduled policy modes — schedule evaluation hardened including overnight
  windows (`internal/models/schedule.go`), with within-tier "actively scheduled
  beats unscheduled" precedence.
- [x] Policy change audit log (`GET /api/logs?kind=policy_changes`, always on).
- [x] SOCKS5 UDP ASSOCIATE (RFC 1928 cmd 3) — DNS through the policy's DoH
  filter, UDP/443 dropped so QUIC can't bypass the pipeline, everything else
  forwarded (`internal/proxy/socks5_udp.go`). Exercised end to end through a
  live TUN, including a real NTP round trip for the generic relay.
- [x] Transparent/gateway mode (plan Deliverable 3, Linux gateway case) -
  `transparent@` listener with SO_ORIGINAL_DST plus an nftables REDIRECT
  manager, verified on hardware with two real clients and three per-client
  policies. See HANDOFF.md's "Gateway mode".
- [x] TUN capture verified on hardware (Debian 13, systemd, unprivileged
  service user + `CAP_NET_ADMIN`). Capture now uses a private routing table
  instead of the main one, tears itself down on shutdown, and applies
  `bypass_cidrs`/`tun_netmask`. See HANDOFF.md's "Verified on hardware" for
  what was reproduced, what was fixed, and what is still unverified
  (Windows, macOS).

## Out of scope

Listed so they don't get re-proposed as missing features:

- **WireGuard listen mode.** `/api/wireguard` is a deliberate 501 stub
  (`internal/mgmtapi/routes_wireguard.go`) that the UI degrades around
  gracefully. Don't implement it, and don't turn it into a 404.
- **Any client-side reimplementation of the filters** (browser extension or
  similar). Deleted once already — it was a second full copy of SafeSearch,
  the URL/DoH filters, the Bayes scorer, and the image classifier, all
  hand-resynced on every engine change. See "Removed: the Firefox extension"
  in [HANDOFF.md](HANDOFF.md). The proxy and the Android app are the supported
  delivery paths.
