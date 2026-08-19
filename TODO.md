# TODO

The feature roadmap. Every open item names why it matters and which files it
touches, so a session can pick one up without re-deriving the context.

Size tags: `S` a day or less, `M` a focused session, `L` multi-session or needs
hardware/an OS not available in this environment.

Before starting anything here, read [CLAUDE.md](CLAUDE.md)'s gotchas — several
items below are one gotcha away from being implemented wrong.

## 1. Filtering coverage gaps

Places traffic escapes the pipeline **today**. These outrank everything below:
each one is a way the filter silently does nothing. Verified against the code,
not speculative.

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

- [ ] **Container image.** `S`
  No Dockerfile in the repo. The binary is static (`CGO_ENABLED=0`, pure-Go
  SQLite and classifiers), so this is close to free and is the obvious server
  deployment path. Needs a volume for `config/`, `policies/`, `certs/`,
  `categories/`, `logs/`.

- [ ] **`/metrics` endpoint.** `S`
  No Prometheus or metrics surface anywhere in the tree. Requests by action,
  blocks by component, classifier latency, upstream errors.

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

- [ ] **HTTPS for the management UI.** `M`
  The mgmt server is plaintext HTTP only — including the login POST. The
  runtime CA can mint its leaf, mirroring what `Engine.dispatchConn`
  (`internal/proxy/engine.go:250`) already does for TLS-wrapped proxy
  listeners.
  Touches: `app.ServeMgmt` (`internal/app/engine.go:120`),
  `internal/mgmtapi/server.go`.

- [ ] **Settings hot-reload.** `L`
  Policies hot-reload; `PUT /api/settings` still needs a restart of
  `webfilter run`. Scope it honestly — listener changes require a rebind, and
  cert/logs directory changes reopen handles, so the deliverable is probably
  "reload what safely can, and tell the UI which fields still need a restart"
  rather than all-or-nothing.
  Touches: `internal/proxy/state/state.go`, `internal/app/engine.go`.

- [ ] **macOS support.** `L (unverifiable here)`
  Absent from the target list in `scripts/package-release.sh:31-33` and from
  `ci.yml`'s cross-compile matrix, and `internal/tun2socks` has no
  `platform_darwin.go` for route/DNS setup (see `packaging/README.md`).

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

## Done

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
