# Handoff: Go port of mitmproxy-web-filter

Status as of this commit: **All 11 phases have real, tested code behind them.** Phase 8 (text
classifier ML stage) is fully implemented and verified end-to-end in this sandbox (pure Go, no
external toolchain needed). Phase 7 (ONNX image classifier) is implemented but only *partially*
verified — see below for the exact boundary of what is and isn't proven to work, and why. This
document is written so a new session (human or AI) can resume without re-deriving context.

## What this project is

A from-scratch Go port of [mitmproxy-web-filter](https://github.com/Yjlion/mitmproxy-web-filter)
(a Python/mitmproxy-based household/office web-filtering proxy), targeting:

- A single Go binary per OS (Windows x86_64, Linux x86_64/arm64) — no Python runtime/venv to bundle.
- **Same `config/settings.json` and `policies/*.json` schema** as the Python original, so existing
  config directories work unmodified.
- **Same Tailwind + Alpine.js web UI**, reused verbatim (`ui/*.html`, `.js`, `.css` are byte-for-byte
  copies from the Python repo's `management/ui/`) — only the backend is rewritten, in a way that
  matches every endpoint path and JSON shape the UI already expects.
- Full feature parity with the Python original, with two explicit exceptions agreed with the user
  up front:
  - **WireGuard proxy-listen mode is out of scope.** `/api/wireguard` returns a `501` stub the
    unmodified settings.html JS already handles gracefully (treats any non-`ok` response as
    `{enabled: false}`, no error shown).
  - **ML classifiers (NSFW image detection, adult-text detection) will use ONNX Runtime via CGO**
    for real functional parity (not a pure-Go heuristic drop-in). This is the *only* place CGO is
    allowed — everything else is pure Go specifically so cross-compilation stays trivial.

The reference Python source lives at `C:\Users\yjlio\Documents\mitmproxy-web-filter` on the
machine this was built on (a sibling checkout, not part of this repo). If you don't have that
checkout, the architecture notes below plus the Python project's own `CLAUDE.md` are the next-best
source of truth for exact filter behavior.

## Where the original plan lives

The full original implementation plan (all 11 phases, library-choice rationale, the CA/cert-issuance
design writeup, ONNX/CGO packaging strategy, etc.) was written to
`C:\Users\yjlio\.claude\plans\i-want-a-port-precious-flurry.md` on the authoring machine. That path
is local to that machine's Claude Code install and **will not be present** in a fresh clone of this
repo or on another machine. Everything load-bearing from that plan is reproduced or summarized in
this document; if you have access to that file it's worth reading for the deeper "why" behind each
design decision, but this document is self-sufficient for resuming work.

## Current status by phase

| Phase | Status | What it covers |
|---|---|---|
| 0. Scaffolding | ✅ Done | `cobra` CLI skeleton, CI cross-compile matrix |
| 1. Models + config store | ✅ Done | `Policy`/`GlobalSettings` structs, atomic file I/O, fsnotify watcher |
| 2. Management API core | ✅ Done | Full REST API + embedded UI serving, browser-verified |
| 3. CA + cert issuance | ✅ Done | Root CA generation, per-host leaf certs, import/export |
| 4. Minimal proxy engine | ✅ Done | Plain HTTP + CONNECT blind-splice passthrough |
| 5. MITM + pipeline skeleton | ✅ Done | Real TLS interception, `FlowContext`, ordered `Pipeline` |
| 6. Filtering addons | ✅ Done | All 12 addons ported from Python, unit-tested |
| 7. ONNX image classification | 🟡 Implemented, partially verified | `internal/classify/image` (YOLOv8-style ONNX decode, `yalue/onnxruntime_go`-backed, build tag `onnx`) wired into `buildProxyEngine`; pure-Go helpers (letterbox/normalize/decode/labels) unit-tested, but the actual `-tags onnx` code path has never been compiled or run against a real model in this sandbox (see below) |
| 8. Text classifier ML stage | ✅ Done | `internal/classify/text`: TF-IDF + logistic-regression scorer, pure Go, JSON sidecar model format, offline trainer (`scripts/train_text_classifier.go`), wired into `buildProxyEngine`; fully unit-tested including an end-to-end train→score round trip |
| 9. Categories, neighbors/ARP | ✅ Done | `internal/categories` + `internal/neighbors` built and used by the proxy; management-API endpoints live: `GET /api/categories` (real index data), `GET /api/tools/{neighbors,public-ip}`, `POST /api/tools/{youtube,doh,scan}`, `GET /api/logs/export` (CSV + pure-Go XLSX); `webfilter oui update` (real IEEE OUI dataset → `neighbors.Entry.Vendor`) and `webfilter categories update` (real IPFire squidGuard blocklist → `categories/*/domains` + `index.json`) both implemented and verified against live upstream data |
| 10. Hardening, packaging | ✅ Done | Native Windows service (`webfilter service install/start/stop/uninstall/status`), Linux systemd units + installer (`packaging/`), release-archive packaging (`scripts/package-release.sh` + a CI `release` job on `v*` tags) |

`go build ./...`, `go vet ./...`, and `go test ./...` are all green as of this commit. `webfilter
run`/`webfilter proxy` now perform **real MITM interception and real filtering** for every addon.
The text classifier's ML stage (Phase 8) is fully wired: set
`GlobalSettings.TextClassifierModelPath` to a trained sidecar to get real ML scoring on top of the
always-on keyword pre-filter; leave it empty for keyword-only (today's default, zero config). The
image classifier's ONNX backend (Phase 7) is wired the same way via
`GlobalSettings.ImageClassifierModelPath`, but **only build it with `-tags onnx`** - the default
build's stub always fails open (passthrough) regardless of that setting, and even the `-tags onnx`
build is unverified beyond compiling on CI (see the Phase 7 section below before trusting it in
production).

## Verification already done (don't redo blindly — but do re-verify after big changes)

- **Config round-trip**: `internal/models/roundtrip_test.go` unmarshals the *actual*
  `config/settings.json` / `policies/*.json` / `settings.example.json` copied from the Python repo
  (secrets redacted) and asserts idempotent round-trip + specific field values, including the
  legacy SafeSearch flat-schema migration exercised against the real `policies/default-copy.json`
  fixture (which still uses the pre-`engines`-map format).
- **Management API**: browser-tested end-to-end via Claude's preview tooling — dashboard load,
  policy create/edit/delete, settings save, PAC generation, full auth flow (enable password, wrong
  password rejected, correct password accepted, logout invalidates session), logs page, analytics
  page — zero console errors, zero UI file changes. Also covered by `httptest`-based Go tests in
  `internal/mgmtapi/server_test.go` and `certs_routes_test.go`.
- **TLS chain validation**: `internal/certs/certs_test.go`'s
  `TestFullTLSHandshakeAgainstGeneratedCA` spins up a real `tls.Listener` using the leaf-cert
  `GetCertificate` callback and dials it with `crypto/tls.Dial` + a `RootCAs` pool containing the
  generated CA — the Go-native equivalent of `openssl s_client -connect ... -servername
  example.com`. Full chain verification passes.
- **Minimal proxy engine (Phase 4)**: `internal/proxy/engine_test.go` covers plain HTTP forwarding
  (`httptest.Server` origin dialed through the proxy via `http.ProxyURL`), the CONNECT blind-splice
  tunnel (`httptest.NewTLSServer` origin, real TLS handshake over the tunnel verified against the
  origin's cert), and that unsupported `proxy_listen` modes (e.g. `socks5@`) are skipped rather than
  failing the whole engine. Also manually verified end-to-end with a real `curl -x` against
  `https://example.com` (both HTTP and HTTPS/CONNECT) through the built `webfilter proxy` binary.
- **MITM interception (Phase 5)**: `internal/proxy/engine_test.go`'s
  `TestMitmInterceptionIssuesOwnLeafCertificate` does a real CONNECT+TLS handshake through the
  engine against an `httptest.NewTLSServer` origin, captures the leaf certificate actually
  presented via `TLSClientConfig.VerifyPeerCertificate`, and asserts it chains to the *engine's own*
  CA and is NOT the origin's real certificate - i.e. genuine interception, not a passthrough.
  `TestMitmBypassStillBlindSplices` confirms a policy with `mitm.mode: exclude` for a host still
  gets the untouched blind splice instead.
- **All 12 addons (Phase 6)**: every addon has a dedicated `_test.go` in `internal/proxy/addons/`
  (70+ test cases total) covering the tricky logic paths from the Python originals - CIDR/MAC
  policy-matching tiers, category blacklist/whitelist, safesearch per-engine tab blocking + image-CDN
  wholesale block, DOH NXDOMAIN/EDE/sinkhole-IP classification (via a fake RFC 8484 HTTP server),
  YouTube's nested-JSON player/get_watch/next/browse mutation and HTML regex extraction, proxy-auth's
  CONNECT-vs-per-request gating and `ClientDisconnected` cleanup, and the image classifier's
  blur/checkerboard/block actions (real JPEG/PNG round-trips, not mocked). Also manually verified
  end-to-end: a real policy blocking `*.example.org` via `url_filter`, loaded by `webfilter proxy`,
  correctly MITM-intercepts and serves the block page for `https://www.example.org` while
  `https://example.com` passes through untouched, with rows landing in the SQLite request log.

## Two real bugs found and fixed during Phase 2/3 (worth knowing about)

1. **`http.FileServer` redirect loop**: rewriting `/` → `/index.html` and then handing that to
   `http.FileServer`/`http.FileServerFS` triggers Go's built-in "redirect `/index.html` → `/`"
   canonicalization, causing an infinite redirect loop (and it would also break the UI's own
   `location.href = 'index.html'` navigations, which the live Python server serves as a plain
   `200`). Fixed in `internal/mgmtapi/static.go` by serving embedded files directly via
   `fs.ReadFile` + `http.ServeContent`, bypassing `FileServer` entirely. There's a regression test
   for this (`TestIndexDoesNotRedirectLoop`) — don't reintroduce `http.FileServer`/`FileServerFS`
   for the UI static path without re-reading that comment.
2. **Test isolation via relative config paths**: `GlobalSettings`'s documented defaults for
   `cert_dir`/`policies_dir`/`logs_dir` are relative (`"./certs"` etc.) — matching the Python
   original. A `t.TempDir()`-based test helper that only isolates `settingsPath` does **not**
   isolate those derived directories, since they resolve against the test process's *working
   directory*, not the settings file's location. `internal/mgmtapi/server_test.go`'s
   `newTestServer` now seeds an explicit settings.json with absolute temp-dir paths for those three
   fields before calling `NewServer`. If you add new tests that construct a `Server`/`CA`/
   `PolicyStore`/log store directly, make sure they don't accidentally share `./certs`,
   `./policies`, or `./logs` relative to whatever directory `go test` happens to run from.

## Key architecture decisions (condensed from the original plan)

- **Process model**: one binary, `webfilter run|proxy|mgmt|categories update|oui update|version`.
  `run` launches the proxy engine and management server as two goroutines in one process (the
  default for a single Windows service / systemd unit). `proxy` and `mgmt` remain available
  standalone for operators who want process isolation. **All cross-component communication is
  filesystem-only** (policy/settings JSON, the SQLite log DB) — no in-process RPC even when
  co-located, so hot-reload and the log DB's writer/reader split behave identically regardless of
  deployment topology.
- **MITM proxy engine (Phase 4/5, done)**: hand-rolled `net.Listener` + raw HTTP/1.1 parsing via
  `http.ReadRequest`/`http.Response.Write`-equivalent hand-rolled writers, deliberately **not**
  `elazarl/goproxy` and, as of Phase 5, deliberately **not `net/http.Server` either** - Phase 4
  used `http.Server` + `Hijacker` for convenience, but real MITM needs to own the connection down
  to the TCP/TLS layer (accept the raw `net.Conn`, optionally wrap it in `tls.Server` with our own
  `GetCertificate`, then hand-parse HTTP/1.1 requests off the decrypted stream) so `internal/proxy`
  was rewritten around a plain `net.Listener` Accept loop (`engine.go`) and manual request/response
  framing (`handler.go`). `Engine.Listen()`/`Engine.Serve()` remain split from `Engine.Run()` so
  tests can bind port 0 and discover the real port before serving.
  - **CONNECT handling** (`Engine.handleConnect`): computes the target `host:port`, asks the
    pipeline's `ConnectGate` (only `ProxyAuthGate` implements this) to authorize the tunnel, then
    checks `Runtime.ShouldBypassMitm(host)` - the aggregated `mitm.mode: exclude` domain list from
    every loaded policy (mirrors `_sync_ignore_hosts`/`ctx.options.ignore_hosts`; like the Python
    original, this is **global, not per-source-IP** - a real mitmproxy/TLS limitation, not a
    shortcut). Excluded hosts get the Phase 4 blind splice unchanged. Everything else gets a real
    `tls.Server` handshake using `certs.LeafIssuer.CertificateFor` - falling back from
    `ClientHelloInfo.ServerName` to the CONNECT target host when SNI is blank (true for IP-literal
    HTTPS targets, which never carry SNI per RFC 6066) - advertising only `http/1.1` via ALPN
    (documented h2-out-of-scope decision, still in force). Each inner HTTP/1.1 request read off the
    decrypted stream is normalized to an absolute `https://` URL and run through the exact same
    `handleOneRequest` path as plain-HTTP forwarding.
  - **Runtime** (`internal/proxy/state`): the shared, hot-reloaded state every addon reads -
    replaces the several independent per-addon-module globals the Python original keeps. Loads
    `settings.json` once at startup (matching the Python original: settings changes need a
    restart, only `policies/*.json` hot-reloads); `GetPolicy(clientIP)` ports
    `policy_router.get_policy`'s MAC→exact-IP→CIDR→catch-all tiered matching (using
    `net.IP.Equal`/`net.IPNet.Contains`, which already treat IPv4 and IPv4-mapped-IPv6 forms as
    equal - no manual unwrapping needed, unlike the Python original's explicit `ipv4_mapped`
    handling); `ShouldBypassMitm` is the aggregated MITM-exclude set described above.
- **Addon pipeline (Phase 5/6, done)**: `internal/proxy.Pipeline` holds one fixed-order
  `[]Addon` slice; `RunRequest`/`RunResponse`/`RunError` each just type-assert every addon against
  `RequestAddon`/`ResponseAddon`/`ErrorAddon` and call the hook if implemented - mirroring
  mitmproxy's "call the hook methods an addon actually defines, in registration order" semantics,
  **including** the easy-to-miss detail that setting `fc.Response` early (a block page, a redirect)
  does **not** skip later addons' request hooks - only the real upstream fetch is skipped. Wired up
  in `cmd/webfilter/runners.go`'s `buildProxyEngine`, in `proxy/main.py`'s exact order:
  `ManagementAccess → ProxyAuthGate → PolicyRouter → MitmControl → UrlFilter → QuicBlocker →
  DohFilter → SafeSearch → YouTubeFilter → TextClassifier → ImageClassifier → RequestLogger`.
  All 12 addons live in `internal/proxy/addons/`, one file each, importing `internal/proxy` for
  `FlowContext`/the hook interfaces (no import cycle - the engine never imports `addons`; wiring
  happens one level up, in `cmd/webfilter`). `ProxyAuthGate` additionally implements
  `proxy.ConnectGate` (the CONNECT-stage auth check, since that runs before any `FlowContext`
  exists) and is constructed with the shared `*state.Runtime` directly for that reason.
  `management_access.go`'s `dns_request` hook (pseudo-domain DNS answers for dns-mode/transparent/
  WireGuard deployments) has **no equivalent** - this engine doesn't run a DNS listener, and those
  proxy_listen modes are unimplemented anyway.
  `doh_filter.go` pulled in `github.com/miekg/dns` (new dependency) for RFC 8484 wireformat
  encode/decode + EDNS0/EDE parsing - hand-rolling that wire format wasn't worth it.
  `image_classifier.go` pulled in `github.com/disintegration/imaging` (new dependency, pure Go) for
  Gaussian blur; the checkerboard/block actions and the pixel-dimension gate use only stdlib
  `image`/`image/draw`/`image/color` (the latter mirrors PIL's lazy header-only `Image.open().size`
  read via `image.DecodeConfig`, no full decode).
- **ONNX/CGO image classifier (Phase 7, implemented, partially verified)**: `internal/classify/image`
  implements `addons.ImageDetector` two ways behind a build tag:
  - `detector_stub.go` (`!onnx`, the default): a configured model path returns `ErrNotBuilt`; an
    empty path returns `(nil, nil)` (passthrough) - this is what every release archive ships,
    exactly matching the Python original's fail-open behavior when `nudenet` isn't installed.
  - `detector_onnx.go` (`-tags onnx`): a real `github.com/yalue/onnxruntime_go`-backed detector for
    a YOLOv8-style ONNX object-detection export (the format NudeNet v3's own `*n.onnx` checkpoints
    use). It dynamically loads a companion `onnxruntime.dll`/`.so` at runtime (no static link, per
    `onnxruntime_go`'s own design - see its README) and reads class labels from a sidecar
    `<model>.labels.json` file rather than hardcoding NudeNet's specific class list/order, so it
    isn't coupled to guesses about that model's exact export shape.

  **What is and isn't verified**: the preprocessing/postprocessing helpers (`letterbox`,
  `toCHWFloat`, `decodeYOLOv8`, `loadLabels` - all in files without a build tag) are unit-tested
  directly with synthetic images/tensors and pass. `detector_onnx.go` itself has **never been
  compiled** in this session: this sandbox has no C compiler (no `gcc`, no `zig`) and
  `CGO_ENABLED=0` by default here, and `onnxruntime_go` requires cgo even though it only uses it to
  dynamically load the shared library rather than link against onnxruntime's source. A CI job
  (`build-onnx` in `.github/workflows/ci.yml`, `ubuntu-latest` has `gcc` preinstalled) now compiles
  and vets it on every push - **watch that job on the first push of this commit**, since it's the
  first real compile this code will ever get. Passing that job proves it compiles against
  `onnxruntime_go`'s actual API; it does **not** prove the tensor-shape assumptions
  (`(1,3,size,size)` input, `(1,4+numClasses,numAnchors)` output) are correct for a real NudeNet-v3
  export, since no actual `.onnx` model or `onnxruntime` shared library was available to test
  against end-to-end. If you have both, point `GlobalSettings.ImageClassifierModelPath` at the
  model (with a matching `.labels.json` beside it), build with `-tags onnx`, and check real images
  through the proxy before trusting this in production - it fails open (never flags NSFW) on any
  load error, so a wrong assumption here is more likely to silently under-block than to crash.
- **Text classifier ML stage (Phase 8, done)**: `internal/classify/text` is a pure-Go TF-IDF +
  logistic-regression scorer (no ONNX/CGO needed, as originally anticipated - it's genuinely just a
  dot product + sigmoid): `Model` (JSON sidecar format), `Load`/`Save`, `Vectorize`/`Score`
  (implements `addons.MLScorer`), and `BuildVocab`/`Train`/`TrainModel` for offline training.
  `scripts/train_text_classifier.go` (`//go:build ignore`, run via `go run`) is a thin CLI over
  `TrainModel` that reads a `text,label` CSV corpus and writes the JSON sidecar
  `GlobalSettings.TextClassifierModelPath` points at. Fully unit-tested, including an end-to-end
  `BuildVocab → Vectorize → Train → Score` round trip on a small smoke-test corpus (see
  `train_test.go`'s `demoCorpus`) that also checks generalization to held-out sentences, not just
  memorization of the training rows.

  **What's still a real gap, deliberately not closed here**: `demoCorpus` is intentionally tiny and
  tame (content-warning-style sentences that *name* adult categories, not actual explicit material)
  - it exists to prove the pipeline works, not as a shippable model. Sourcing and labeling a real
  corpus for a production-quality classifier is a genuine, use-case-specific judgment call (what
  counts as "adult" for this deployment, data licensing, avoiding bulk-acquiring actual explicit
  content into this repo) that deserves the operator's own decision - point
  `scripts/train_text_classifier.go` at whatever labeled corpus you assemble.

## Go package layout (as built so far)

```
gowebfilter/
  cmd/webfilter/            # cobra CLI: main.go, cmd_run.go, cmd_proxy.go, cmd_mgmt.go,
                             #   cmd_categories.go, cmd_oui.go, flags.go, runners.go,
                             #   service_windows.go / service_other.go (Windows service, Phase 10)
  internal/
    macutil/                 # MAC address normalization (shared by models + neighbors)
    models/                   # Policy, GlobalSettings, all sub-configs, proxy_listen parser
    config/                    # settings.json + policies/*.json load/save, fsnotify watch wrapper
    logstore/                  # modernc.org/sqlite: schema, single-writer, analytics, prune, export
    pwhash/                     # PBKDF2-SHA256 (mgmt password + proxy-auth password)
    certs/                       # CA generation/import/export, per-host leaf cert issuance + cache
    categories/                   # shared site-category domain blocklists (lazy-load, mtime cache)
    neighbors/                     # cross-platform ARP/NDP reader (Linux/Windows/BSD parsers)
    classify/
      text/                        # Phase 8: TF-IDF + logistic-regression addons.MLScorer, pure Go
      image/                       # Phase 7: addons.ImageDetector - detector_stub.go (default) /
                                    #   detector_onnx.go (-tags onnx, yalue/onnxruntime_go-backed)
    mgmtapi/                        # chi router, auth, all current routes, PAC generator, static UI
    proxy/                           # forward-proxy engine: Engine, Pipeline, FlowContext, matching,
                                     #   block-page render, MITM/CONNECT handling
      state/                         # Runtime: hot-reloaded settings/policies, GetPolicy, CA/leaf
                                     #   issuer, log store, category store - shared by every addon
      addons/                        # all 12 filtering addons, one file each, ported from
                                     #   proxy/addons/*.py
  ui/                          # management UI copied verbatim from the Python repo + embed.go
  config/settings.example.json # shipped template (matches the Python original's)
  policies/default.json.example # shipped template
  .github/workflows/ci.yml     # build+vet+test, cross-compile matrix (pure-Go, CGO_ENABLED=0),
                                #   a `release` job on `v*` tags (Phase 10)
  .claude/launch.json          # local dev-server config for `mgmt` (used with Claude's preview tools)
  packaging/                   # Phase 10: systemd units (webfilter.service, -proxy, -mgmt),
                                #   install.sh, README.md covering both Linux and Windows deployment
  scripts/
    package-release.sh         # cross-compiles + bundles all 3 release targets into tarballs/zip
    archive.go                 # `//go:build ignore` helper - pure-Go tar.gz/zip writer package-
                                #   release.sh shells out to via `go run`, so packaging doesn't
                                #   depend on a host `tar`/`zip` binary being installed
    train_text_classifier.go   # `//go:build ignore` helper - Phase 8 offline trainer, run via
                                #   `go run scripts/train_text_classifier.go corpus.csv model.json`
```

`internal/block/` from the original plan doesn't exist as a separate package - this port's
block-page rendering instead lives in `internal/proxy/block.go`, no separate package was needed.

## How to build, run, and test

```bash
go build ./...          # build everything (default: pure Go, no CGO, image classifier stubbed)
go vet ./...
go test ./...            # full suite across every package

go build -o webfilter.exe ./cmd/webfilter   # produce the CLI binary (Windows)
./webfilter.exe mgmt --settings config/settings.json    # management server only
./webfilter.exe proxy --settings config/settings.json   # forward-proxy engine, full MITM + filtering
./webfilter.exe run --settings config/settings.json     # both, in one process

# Optional: real ONNX-backed NSFW image detector (Phase 7) instead of the
# default passthrough stub. Needs CGO_ENABLED=1, a C toolchain, and the
# onnxruntime shared library available at runtime - see the Phase 7 notes
# above before relying on this.
go build -tags onnx -o webfilter.exe ./cmd/webfilter

# Train the text classifier's optional ML stage (Phase 8) from a labeled
# "text,label" CSV corpus, then point GlobalSettings.TextClassifierModelPath
# at the result:
go run scripts/train_text_classifier.go corpus.csv model.json
```

`webfilter proxy` (or `run`) now does **real MITM interception and real policy-based filtering** -
point a browser (with the generated `certs/ca.crt` imported into its trust store, or accept the
cert warning) or `curl -x http://127.0.0.1:8080 ... --cacert certs/ca.crt` at it. A policy with
`url_filter.enabled=true` and a `block` pattern will actually intercept and block matching HTTPS
sites; everything else passes through with real content. Manually verified end-to-end this session:
a policy blocking `*.example.org` correctly served the block page for `https://www.example.org`
while `https://example.com` loaded normally through the same running proxy, both over a real
CONNECT+TLS-intercepted tunnel (not blind splice) with rows landing in `logs/webfilter.db`.

For local UI testing, `.claude/launch.json` defines a `webfilter-mgmt` preview server
(`go run ./cmd/webfilter mgmt`, port 8000) usable with Claude Code's `preview_start`/`preview_*`
tools. Copy `config/settings.example.json` to `config/settings.json` first (gitignored, so this is
safe to do locally without touching version control).

## Documented deviations from the Python original (intentional, not bugs)

- `GlobalSettings` has one Go-only optional field, `image_classifier_model_path`
  (`omitempty`), not present in the Python schema — path to the NudeNet-compatible ONNX model file
  once Phase 7 lands. Round-trips harmlessly through the Python original since it doesn't validate
  unrecognized `settings.json` keys.
- `proxy_listen` entries with a `wireguard@` prefix parse without error (recognized-but-unsupported
  mode) rather than crashing, so a settings.json carried over from the Python original with a
  leftover WireGuard listener doesn't fail to load — that entry is just never started.
- HTTP/2 over MITM'd connections is **out of scope for v1** (only `http/1.1` is advertised via ALPN
  on intercepted connections, enforced in `internal/proxy/handler.go`'s `handleConnect`) - this
  avoids a large amount of HPACK/stream-multiplexing complexity interacting with the addon
  pipeline. Real behavioral difference from Python mitmproxy (which supports h2); flag to the user
  if it becomes a problem in practice (a client that refuses to fall back to h1 would fail here).
- fnmatch-style glob patterns (`url_filter.block`/`allow`, `mitm.sites`, etc.) are matched
  **case-sensitively on every platform** (`internal/proxy/matching.go`'s `fnmatch`), rather than
  reproducing Python's `fnmatch.fnmatch`, whose case-sensitivity actually varies by OS (via
  `os.path.normcase` - case-sensitive on Linux, case-insensitive on Windows). A single predictable
  behavior across the Windows/Linux/arm64 build matrix seemed preferable to silently reproducing
  that OS-dependent quirk.
- `neighbors.Entry.Vendor` (IEEE OUI vendor name) is populated by `webfilter oui update`, which
  downloads the Wireshark-maintained manuf list, parses it (`neighbors.ParseWiresharkManuf`), and
  writes it to `GlobalSettings.OuiPath` (`internal/neighbors.DefaultOuiPath` -
  `"./data/oui.txt"` - when unset) via `neighbors.WriteOuiFile`. `neighbors.Lookup` (used by policy
  MAC-tier matching) never needed vendor data and was always fully functional regardless.

## A correction: this environment *does* have internet access

Earlier revisions of this document (and the session that wrote them) assumed no internet access
and left `categories update`/`oui update` as stubs on that basis. That assumption was wrong for at
least this session - both are now implemented and verified end-to-end against live upstream data:

- `oui update` against `https://www.wireshark.org/download/automated/data/manuf` (39,420 entries
  parsed, `VendorFor` round-tripped against real prefixes like Apple's `00:03:93` and Cisco's
  `00:00:0c`).
- `categories update` against `https://dbl.ipfire.org/lists/squidguard.tar.gz` (14 real categories,
  ~1.6M total domains - `ads` 160k, `phishing` 610k, `porn` 529k, etc. - written to `categories/`
  and re-read successfully through `categories.Store`).

If you're picking this project up fresh, **check for internet access before assuming a gap is
blocked** - don't take this document's "blocked" claims at face value where connectivity is the
stated reason.

## Suggested next step

Both Phase 7 and Phase 8 now have real code behind them (see the phase table and the detailed Phase
7/Phase 8 notes above) - what's left is narrower than "implement the backend":

1. **Phase 7**: watch the `build-onnx` CI job on the first push of this commit - it's the first
   time `detector_onnx.go` will ever be compiled (this dev sandbox has no C toolchain). If it's
   green, the next real step is sourcing a NudeNet-v3-compatible `.onnx` model plus its
   `onnxruntime` shared library and a matching `.labels.json`, building with `-tags onnx`, and
   checking real images through the proxy end-to-end - none of the tensor-shape assumptions have
   been checked against an actual model yet, only against synthetic test data.
2. **Phase 8**: the pipeline is done and verified; the only remaining piece is an operator
   assembling and labeling a real, properly licensed adult-content-vs-not corpus (deliberately not
   done here - see the Phase 8 notes above for why) and running
   `go run scripts/train_text_classifier.go` against it.
3. **Optional follow-on, not part of either phase as originally scoped**: `POST /api/tools/scan`
   (the management UI's ad-hoc URL scanner) still returns 503 - not because the classifiers are
   unbuilt anymore, but because `mgmtapi.Server` is constructed independently from the proxy
   engine's addon pipeline and has no loaded classifier instance to call. Wiring that through (e.g.
   passing a shared `*text.Model`/`image.ImageDetector` into both `buildProxyEngine` and
   `mgmtapi.NewServer`) is a reasonable enhancement but wasn't part of Phase 7/8's own scope.

**Phase 9 is fully done** (see the status table): `GET /api/categories` returns the real
`categories/index.json` data, `GET /api/tools/neighbors` powers the policy editor's MAC scan
picker off `internal/neighbors.Scan()` (now with real `Vendor` data), `POST /api/tools/{youtube,doh}`
and `GET /api/tools/public-ip` are live diagnostic tools, `POST /api/tools/scan` returns a clear 503
(see point 3 above), `GET /api/logs/export` streams CSV or a hand-rolled
pure-Go XLSX (validated against openpyxl), `webfilter oui update` populates the vendor lookup table
from the real Wireshark manuf list, and `webfilter categories update` populates `categories/` from
the real IPFire squidGuard blocklist (`internal/categories.ExtractDomainLists`/`WriteCategories`,
stdlib `archive/tar` + `compress/gzip`, no new dependency; picks whichever top-level archive
directory has the most `<name>/domains` entries rather than hardcoding `blacklists/`, and stages
each category fully before an atomic `os.Rename` swap). All covered by
`internal/mgmtapi/routes_phase9_test.go`, `internal/neighbors/oui_test.go`, and
`internal/categories/update_test.go`. The `/api/tools/doh` handler reuses the DoH addon's
wire-query logic via the new exported `addons.QueryDohDetailed`.

**Phase 10 is now done too.** Three pieces:

1. **Native Windows service** (`cmd/webfilter/service_windows.go`, build-tag `windows`;
   `service_other.go` is the `!windows` stub that points Linux users at systemd instead).
   `webfilter service install --settings <path>` registers `webfilter run --settings <abs-path>`
   with the SCM (`golang.org/x/sys/windows/svc/mgr`, `mgr.StartAutomatic`); `start`/`stop`/
   `uninstall`/`status` round out management. The actual service body
   (`webfilterService.Execute`, implementing `svc.Handler`) just calls the existing
   `runProxyAndMgmt(ctx, settingsPath)` on a goroutine and cancels its context on a Stop/Shutdown
   control request - `runProxyAndMgmt`'s signature was changed from `(*cobra.Command, string)` to
   `(context.Context, string)` for this (cosmetic, one call site: `cmd_run.go`). Detection of
   "am I running under the SCM" is `svc.IsWindowsService()`, checked inside `run`'s `RunE` itself
   (not hijacked in `main()`) - so `webfilter run --settings X` behaves identically whether a human
   typed it or the SCM launched it as the installed service's `ExecStart`.
   **Verification**: builds and cross-compiles cleanly (native Windows build plus all three
   `GOOS`/`GOARCH` CI targets); the `service status`/`install`/etc. error path was exercised without
   admin rights and produces a clear "try running as Administrator" message rather than a crash or
   confusing panic. **Not verified**: an actual install→start→stop→uninstall cycle against a live
   SCM. This session does have Administrator elevation available (confirmed via `Start-Process
   -Verb RunAs`), but registering even a temporary test service is a real system-level, auto-start
   persistence action - the harness's permission layer correctly declined to let that happen
   without the user explicitly opting in per-instance, and the user chose not to (rather than
   re-litigate that, this doc just records the outcome). If you have admin on the target machine,
   that end-to-end path (`service install` → `start` → confirm it's actually serving → `stop` →
   `uninstall`) is worth running once yourself before trusting it in production.
2. **Linux systemd packaging** (`packaging/`): `webfilter.service` (combined `run` mode, the
   recommended default) plus `webfilter-proxy.service`/`webfilter-mgmt.service` (split mode, for
   operators who want process isolation - mirrors the Python original's own two-service split).
   `packaging/install.sh --mode run|split [--prefix DIR] [--binary PATH]` creates a system
   `webfilter` user, lays out `/opt/webfilter/{config,policies,certs,categories,logs,data}`, seeds
   `config/settings.json`/`policies/default.json` from the shipped examples if absent, and
   `systemctl enable`s the chosen unit(s). **Verification**: shell syntax checked (`bash -n`); the
   `useradd`/`systemctl` control flow could not be exercised end-to-end since this sandbox is
   Windows (no systemd) - worth a real run on a Linux box before relying on it.
3. **Release archives**: `scripts/package-release.sh [VERSION] [OUT_DIR]` cross-compiles all three
   targets (`windows/amd64`, `linux/amd64`, `linux/arm64`, `CGO_ENABLED=0`, default build tags -
   i.e. without `-tags onnx`, matching CI's cross-compile job exactly) with `-ldflags` injecting
   `internal/version.{Version,Commit,
   BuildDate}`, bundles each with `settings.example.json`, `default.json.example`, and the relevant
   `packaging/` files, and archives them - `.tar.gz` for Linux, `.zip` for Windows, written via a
   small pure-Go helper (`scripts/archive.go`, `//go:build ignore`) rather than shelling out to a
   host `tar`/`zip` binary, since this dev sandbox's git-bash has `tar` but no `zip`.
   `.github/workflows/ci.yml` gained a `release` job that runs this script and attaches the
   archives via `softprops/action-gh-release@v2` whenever a `v*` tag is pushed. **Verification**:
   ran the full script locally, produced all three archives, inventoried both the `.tar.gz` (via
   real `tar tzf`) and the `.zip` (via a throwaway Go `archive/zip` reader, since `unzip` also isn't
   on this box) and confirmed correct contents per platform (systemd files only in the Linux
   archives), then extracted the Windows zip and ran the actual `webfilter.exe version` to confirm
   the ldflags-injected version/commit/build-date string is correct. The CI YAML was validated with
   `yaml.safe_load` (via the reference Python repo's venv) but **the `release` job itself has never
   actually run in GitHub Actions** - worth watching the first real tag push.

All 11 phases now have real, tested code behind them. There is no more "next obvious phase" -
remaining work is: watch `build-onnx` on its first CI run and, if green, source a real NudeNet-v3
ONNX model to validate Phase 7's tensor-shape assumptions end-to-end (a licensing/quality judgment
call worth the operator's own sign-off, same as before); assemble a real labeled corpus to get a
production-quality Phase 8 model (ditto); optionally wire the mgmt API's `/api/tools/scan` into the
now-real classifiers; or general hardening/bug-fixing as issues surface in real use.
