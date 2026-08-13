# Plan: Android Port and Transparent Mode

> **Historical note.** This plan originally had a third deliverable, a
> standalone Firefox WebExtension. It was built, then removed — see HANDOFF.md's
> "Removed: the Firefox extension". The numbering below has been re-flowed;
> Deliverable 3 (transparent mode) keeps its original number for continuity with
> the sections that reference it.

## Context

`gowebfilter` today is a desktop/server MITM forward proxy: clients point at
`:8080` (HTTP) or `:1080` (SOCKS5), the Go engine intercepts TLS with its own
CA, and an ordered addon pipeline filters traffic (URL/category, safesearch,
YouTube rewrite, DoH, QUIC, text + image NSFW classifiers). Reaching users on
phones and inside a browser today requires manual proxy + CA setup, which is
fragile and, on modern Android/Chrome, increasingly blocked.

This document plans two independent-but-related deliverables:

1. **Android app** — a full-feature on-device port using `VpnService`, embedding
   the existing Go engine via gomobile so no external proxy is needed.
3. **Transparent mode** — a new listener/interception path so traffic can be
   captured without per-client proxy configuration, planned per platform and per
   use case (local device vs. network gateway).

**Status:** Deliverable 1 (Android) is scaffolded in code: the shared Go
refactor (`internal/app`), the gomobile `mobile/` package, the offline UI
vendoring, and the Kotlin/Gradle `android/` app all landed; a manual GitHub
workflow (`.github/workflows/android.yml`) builds the debug APK on demand.
Deliverable 3 (transparent mode) remains research/advisory. See `HANDOFF.md`'s
"Android port" section for what is verified vs. not.

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
  design is the right shape.

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
3. **Transparent mode:** implement the `transparent` listener + `SO_ORIGINAL_DST`
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
- **Transparent mode:**
  - Linux: add an `iptables REDIRECT` for a test uid, run `webfilter run` with a
    `transparent@:port` listener, curl a target *without* proxy env vars, confirm
    the request appears in the logs with the correct policy/component and that TLS
    flows to a CA-trusting client are intercepted while a non-trusting client is
    blind-spliced (not broken).
  - Verify upstream/proxy self-traffic is exempted (no redirect loop).
