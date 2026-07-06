# Plan: Android Port, Firefox Extension, and Transparent Mode

## Context

`gowebfilter` today is a desktop/server MITM forward proxy: clients point at
`:8080` (HTTP) or `:1080` (SOCKS5), the Go engine intercepts TLS with its own
CA, and an ordered addon pipeline filters traffic (URL/category, safesearch,
YouTube rewrite, DoH, QUIC, text + image NSFW classifiers). Reaching users on
phones and inside a browser today requires manual proxy + CA setup, which is
fragile and, on modern Android/Chrome, increasingly blocked.

This document plans three independent-but-related deliverables:

1. **Android app** — a full-feature on-device port using `VpnService`, embedding
   the existing Go engine via gomobile so no external proxy is needed.
2. **Firefox extension** — a *standalone, no-proxy* in-browser filter (HaramBlur
   style) that reproduces the "similar features" set client-side.
3. **Transparent mode** — a new listener/interception path so traffic can be
   captured without per-client proxy configuration, planned per platform and per
   use case (local device vs. network gateway).

**Nothing here is committed to code yet** — this is a research/advisory plan.

Key research findings that shape the design:

- The interception core (`internal/proxy`, `internal/certs`, `internal/proxy/state`,
  `internal/config`, `internal/models`, `internal/classify/*`, `internal/categories`,
  `internal/logstore`) is **stdlib-only, no CGO, no OS coupling**, and is already
  embeddable as a library. `cmd/webfilter/runners.go:buildProxyEngine` is the
  entry-point template.
- `internal/proxy/handler.go:handleTunnel(conn, reader, targetHost, hostOnly, …, ready)`
  is the single clean seam every capture front-end (CONNECT, SOCKS5, and future
  transparent/TUN) already funnels into.
- **Transparent mode is greenfield**: `transparent@host:port` is a recognized
  mode string that the engine parses then *skips* (`engine.go:78-81`). No
  `SO_ORIGINAL_DST`/TPROXY code exists yet.
- gomobile + gvisor-netstack tun2socks AAR builds for `android/arm64` are proven
  in production (Teapod, MasterDnsVPN, firestack). `modernc.org/sqlite` is the one
  "pure-Go but verify-on-android" dependency.
- **Android CA trust is the hard ceiling** on full MITM: Android 7+ only trusts
  user CAs in apps that opt in via network-security-config; Chrome ≥99 enforces
  Certificate Transparency and rejects user CAs outright; Firefox needs its
  "Use third-party CA certificates" toggle. This is why the on-device VpnService
  design and the standalone Firefox extension are the right shapes.

---

## Deliverable 1 — Android app (VpnService + gomobile)

### Architecture

```
┌─────────────── Android app (Kotlin) ───────────────┐
│  VpnService.Builder.establish() ──► TUN fd          │
│  Management UI (WebView → embedded mgmt server,      │
│                 or native Compose screens)           │
│  CA install flow (Settings intent + guidance)        │
└───────────────┬─────────────────────────────────────┘
                │ JNI (gomobile .aar)
┌───────────────▼─────────────────────────────────────┐
│  Go engine (reused as-is)                            │
│   tun2socks (fd://TUN) ──► SOCKS5 127.0.0.1:1080 ──► │
│   proxy.Engine → addon pipeline → CA/leaf certs      │
│   state.Runtime (policies, categories, logstore)     │
└─────────────────────────────────────────────────────┘
```

Whole-device traffic → TUN → gvisor netstack terminates each flow in Go →
dials the in-process SOCKS5 listener → existing `handleTunnel` MITM path →
addon pipeline. No external proxy, no root.

### Work breakdown

**A. Go: `mobile` build target (new package, e.g. `mobile/`)**
- Add a gomobile-bound package exporting a tiny API surface: `Start(configDir string, tunFd int) error`, `Stop()`, `ReloadPolicies()`, `Status() string`, and setters the Kotlin layer needs. Keep the surface small — gomobile only binds simple types.
- Internally replicate `buildProxyEngine` (lift it into a shared helper both `cmd/webfilter` and `mobile` call, so the wiring order stays single-sourced): `state.New` → ordered `proxy.NewPipeline([...])` → `Engine{}` struct literal → `Listen()`/`Serve(ctx)`.
- Point all paths at the app private dir passed from Kotlin: set `CertDir`, `PoliciesDir`, `CategoriesDir`, `LogsDir` in the settings JSON to `context.getFilesDir()` subpaths. No engine code change — these are already path-driven (`models.NewGlobalSettings`, `state.New`).
- **tun2socks Android wrapper (~20 lines, new):** do NOT reuse `internal/tun2socks/manager.go` (its `Start` is gated on `GOOS ∈ {windows,linux}` + `geteuid()==0` + shells out to `ip`). Instead call `tunengine.Insert(&engine.Key{Device: "fd://<fd>", Proxy: "socks5://127.0.0.1:1080", MTU: 1500, ...})` + `tunengine.Start()`/`Stop()` directly with the VpnService fd. Keep the reusable pure parts: `selectProxy`, `ValidateConfig`, `ensureTunSocksListener` (auto-adds `socks5@127.0.0.1:1080`).
- Build: `gomobile bind -target=android/arm64,android/arm -androidapi 26 -o webfilter.aar ./mobile`.

**B. Kotlin/Android app**
- `VpnService` subclass: build the TUN (`addAddress` 198.18.0.1/15 range as in `Tun2SocksConfig` defaults, `addDnsServer`, `addRoute 0.0.0.0/0`, `establish()`), pass `fd.detachFd()` to Go `Start()`. Handle revoke/reconnect, `onDestroy` → `Stop()`.
- **Per-app filtering (Android-specific win):** expose `addAllowedApplication`/`addDisallowedApplication` so users pick which apps are filtered — this is the Android analogue of the desktop per-client policy tiers (on a single-user device, source-IP tiers collapse to catch-all, so per-app is the meaningful axis).
- **Management UI:** cheapest path is to keep serving the existing embedded chi mgmt server on `127.0.0.1:8000` from Go and load it in a WebView. Caveat: vendor Alpine.js (currently a CDN `<script>`) into `ui/` so it works offline — one-time change, also benefits desktop offline use. Alternative (more work, nicer): native Compose screens hitting the same REST API.
- **CA install flow:** guide the user through installing `ca.crt` (exported via existing `GET /api/ca-cert`). Document the ceiling clearly in-app: user CAs are trusted by apps that opt into user certs; **Chrome and many hardened apps reject the user CA (Certificate Transparency)** and will fail or bypass. Position on-device filtering honestly: URL/host/SNI/DoH filtering works for everything routed through the TUN even without trust; deep body features (text/image classifier, YouTube rewrite) only work for apps that accept the CA. Blind-splice untrusted flows rather than breaking them (the engine already does this via `ShouldBypassMitm`/first-byte sniff).

**C. Classifiers**
- **Per the decision: include the 8.6 MB MobileNetV2 CNN, opt-in per policy (off by default), same as desktop.** It's embedded via `//go:embed model.bin` and runs in the pure-Go inference engine (`nn.go`), already mutex-serialized with a skin-ratio prefilter that short-circuits most images — acceptable on arm64 but validate real-device latency/battery and keep it behind `image_classifier.enabled`. Text Bayes model is ~3 KB, negligible.

### Risks / validate first
1. `modernc.org/sqlite` (+`modernc.org/libc`) on `android/arm64` — build a throwaway AAR calling logstore before committing. Fallback: stub logging on mobile if it fights the toolchain.
2. Confirm `fd://` device scheme + exact `engine.Key` fields against pinned `xjasonlyu/tun2socks v2.6.0` source.
3. Exclude desktop-only deps from the mobile build: all `cmd/webfilter` (cobra, `gogpu/systray`, `godbus`, Windows service), and `internal/tun2socks/platform_*`/route-setup files.
4. On-device image CNN latency/battery under real browsing.

---

## Deliverable 2 — Firefox extension (standalone, no proxy)

Per the decision: a self-contained in-browser filter modeled on **HaramBlur**
(client-side NSFW blur), *not* a proxy companion. It reproduces "similar
features" using browser APIs + client-side ML. **This is a new, separate
codebase** (TypeScript/JS WebExtension), sharing *concepts and data assets* with
the Go project but not code.

### Feature mapping (Go addon → in-browser mechanism)

| Go feature | Firefox mechanism | Fidelity |
|---|---|---|
| url_filter (allow/block + categories) | `declarativeNetRequest` static + dynamic rulesets, compiled from category domain lists | High |
| safesearch (google/bing/ddg/yahoo/youtube, tabs) | `declarativeNetRequest` redirect + modifyHeaders rules; already request-side in Go | High |
| doh_filter | `declarativeNetRequest` block of known DoH endpoints + steer to filtering resolver; optionally toggle Firefox's own DoH | Medium |
| quic_blocker | N/A in-browser (Firefox honors its own settings); can drop Alt-Svc via DNR modifyHeaders | Low/moot |
| image_classifier (NSFW) | **content script + NSFWJS** (TensorFlow.js) on `<img>`/`<video>`/CSS backgrounds; blur/hide overlay; hover-to-unblur. Same GantMan/nsfw_model family the Go side embeds | High (this is the HaramBlur core) |
| text_classifier (adult text) | content-script keyword prefilter + optional lightweight client scorer over visible text; port the Bayes `model_data.json` (~3 KB) to JS | Medium |
| youtube_filter (channel block, strip comments/recs) | content script on youtube.com: hide/remove DOM nodes by channel id/@handle/name, comments section, recommendation sidebar/home feed. DOM-level, not response rewrite | Medium-High |
| schedule / policy | extension storage + `alarms`; per-profile (no per-client tiers) | High |
| block_page | extension-served block page / redirect | High |
| policy_router / mitm_control / proxy_auth / mgmt_access | N/A — proxy infrastructure, no analogue in a per-browser extension | — |

### Technical approach (HaramBlur-derived)
- **Manifest V3, Firefox flavor.** Firefox MV3 *keeps* blocking `webRequest` and
  `webRequestBlocking`, but prefer `declarativeNetRequest` for URL/safesearch
  (faster, survives service-worker suspension). Use `webRequest` only where DNR
  can't express a rule.
- **NSFW detection:** NSFWJS + TensorFlow.js (WebGL backend, WASM fallback). Run
  inference off the main thread (web worker / offscreen where available) to keep
  pages responsive. Cache verdicts per image URL/hash; process video by sampling
  frames to a canvas on an interval. Face detection (vladmandic/human) is
  optional — include only if "blur faces / gaze" is in scope.
- **Content script** with a `MutationObserver` to catch lazy-loaded/dynamic
  media (infinite-scroll feeds), a size/skin prefilter before running the CNN
  (mirror the Go skin-ratio gate to save CPU), and a shared blur/overlay UI.
- **Category/blocklist assets:** reuse the IPFire squidGuard category lists the
  Go `categories update` command already consumes — compile them into DNR JSON
  rulesets at build time. This keeps the two products' block data in sync.
- **UI:** popup (on/off, blur strength, per-feature toggles, hover-to-unblur) +
  options page (categories, safesearch engines, YouTube channel lists, schedule),
  persisted in `browser.storage`.
- Ships on AMO; a Chromium build is possible later but Chrome MV3 drops blocking
  `webRequest` — DNR + content-script classifier still work there.

### Notes
- All processing is on-device/in-browser (privacy parity with HaramBlur and the
  Go project). No CA, no proxy, no network round-trips for classification.
- This does **not** replace the proxy for non-Firefox apps — it's a per-browser
  layer. Users wanting whole-device coverage use Deliverable 1 (Android) or the
  desktop proxy.

---

## Deliverable 3 — Transparent mode (per platform × use case)

Goal: intercept without configuring a proxy on each client. All paths converge on
the existing `handleTunnel(conn, reader, targetHost, hostOnly, …, ready)` seam —
the work is the *front-end* that recovers the original destination and hands a
connection to that seam. Add a `transparent` listener implementation in
`internal/proxy` (today the mode is parsed then skipped at `engine.go:78-81`).

### Common Go work
- Implement a transparent listener: after `Accept()`, recover the original
  destination and call the existing intercept path with an explicit target host.
- **Original-destination recovery, two techniques:**
  - `SO_ORIGINAL_DST` via `getsockopt` (`syscall`, Linux/Android) for **REDIRECT**
    (NAT) setups — connection arrives locally, kernel remembers the real dst.
  - `IP_TRANSPARENT` + `TPROXY` for non-NAT inline setups (preserves client IP;
    needs `CAP_NET_ADMIN` + policy routing).
- SNI is still available for TLS flows, so cert selection is unchanged; for
  non-SNI/IP-literal flows fall back to the recovered dst (handler already does
  this). This front-end is small and OS-guarded with build tags
  (`//go:build linux` etc.); keep it out of the gomobile build.

### Matrix

| Platform | Use case | Mechanism | Notes |
|---|---|---|---|
| **Android** | Local (on-device) | **VpnService TUN** (Deliverable 1) | *This is already transparent capture* — no per-app proxy config; tun2socks feeds `handleTunnel`. Nothing extra beyond D1. |
| **Linux** | Local device | `iptables/nft REDIRECT` → transparent listener + `SO_ORIGINAL_DST` | Redirect outbound 80/443 to the proxy port; run proxy as a dedicated uid to avoid loops. Root/`CAP_NET_ADMIN`. |
| **Linux** | Network gateway (router/box filters a LAN) | `TPROXY` (`IP_TRANSPARENT`) or REDIRECT on the forwarding path | Preserves client source IP → **per-client policy tiers (IP/CIDR/MAC) work fully here**, the strongest fit for the existing policy model. Needs policy routing + `CAP_NET_ADMIN`. Pair with QUIC block + DoH filter so clients can't bypass. |
| **Linux/any** | Network, no gateway control | **PAC / WPAD** (already served: `/proxy.pac`, `/wpad.dat`) or DHCP option 252 | Not "transparent" at packet level but zero per-client manual proxy entry. Already implemented in mgmt API — document as the low-privilege option. |
| **macOS** | Local | `pf` `rdr` → transparent listener + divert/`SO_ORIGINAL_DST`-equivalent | Or a `utun` + gvisor path mirroring Android. `pf` rdr is the standard analogue. |
| **Windows** | Local | **WFP / WinDivert** redirect, or a `wintun` TUN (tun2socks already supports wintun) | The wintun path reuses `internal/tun2socks` (Windows branch) → SOCKS5 → engine; the transparent-listener path needs WinDivert to recover original dst. TUN route is simpler and already partly wired. |
| **Windows/macOS/Linux** | Local, no admin | System proxy / PAC + CA install | Fallback where transparent capture needs privileges the user lacks. |

### Cross-cutting caveats to document
- **CA trust is required for body inspection on every transparent path.** Transparent
  capture routes bytes; it does not make clients trust the CA. Chrome/Android CT
  and app pinning still bypass MITM — blind-splice those flows (engine already
  does). URL/SNI/DoH/QUIC-block features work transparently *without* trust; deep
  features need the CA installed per client/app.
- **QUIC/HTTP3 bypass:** transparent TCP capture misses UDP/443. Keep
  `quic_blocker` (strips Alt-Svc) enabled and, on gateway setups, drop UDP/443 at
  the firewall so clients fall back to interceptable TCP/TLS.
- **Loop prevention:** the proxy's own upstream connections must be exempted from
  the REDIRECT/TPROXY rules (dedicated uid or fwmark).

---

## Recommended sequencing

1. **Shared Go refactor:** extract `buildProxyEngine`'s wiring into a reusable
   helper callable from `cmd/webfilter` and a new `mobile/` package (single-source
   the fixed pipeline order). Vendor Alpine.js into `ui/` for offline use.
2. **Android v1:** gomobile AAR + VpnService + fd tun2socks wrapper + WebView mgmt
   UI + CA install flow + per-app selection. Include image CNN opt-in. Validate
   the three risks (sqlite-on-android, `fd://` Key fields, real-device CNN cost).
3. **Firefox extension:** standalone MV3 with DNR (url/safesearch, from IPFire
   lists) + NSFWJS content-script blur + YouTube content script + options/popup.
4. **Transparent mode:** implement the `transparent` listener + `SO_ORIGINAL_DST`
   (Linux local REDIRECT first — highest value/lowest complexity), then gateway
   TPROXY, then macOS `pf` and Windows WinDivert/wintun. Android is delivered by #2.

---

## Verification

- **Go core / mobile:**
  - `CGO_ENABLED=0 go build ./...`, `go vet ./...`, `go test ./...` stay green after
    the `buildProxyEngine` refactor (guards: `internal/proxy`, `.../addons`, `cmd/webfilter`).
  - Prototype AAR: `gomobile bind -target=android/arm64 ./mobile`; smoke-test on a
    device/emulator that `Start(dir, fd)` brings up the engine and a browser routed
    through the TUN gets filtered (check `GET /api/logs?kind=requests` for
    `action`/`component`, and `?kind=blocks`).
  - Confirm blocked responses are HTTP 200 block pages (per repo gotcha) — don't
    assert on status codes.
- **Firefox extension:**
  - `web-ext run` / `web-ext lint`; load temporary add-on. Verify: a blocklisted
    category domain is blocked (DNR), safesearch params injected on each engine,
    NSFW test images blur without freezing an infinite-scroll page, YouTube channel
    hidden and comments/recs removed. Confirm all inference is local (no network
    calls from the classifier).
- **Transparent mode:**
  - Linux: add an `iptables REDIRECT` for a test uid, run `webfilter run` with a
    `transparent@:port` listener, curl a target *without* proxy env vars, confirm
    the request appears in the logs with the correct policy/component and that TLS
    flows to a CA-trusting client are intercepted while a non-trusting client is
    blind-spliced (not broken).
  - Verify upstream/proxy self-traffic is exempted (no redirect loop).
