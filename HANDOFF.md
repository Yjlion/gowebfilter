# Handoff: Go port of mitmproxy-web-filter

Status as of this commit: **All 11 phases have real, tested code behind them.** Phase 7 (image)
and Phase 8 (text) are real, non-optional, pretrained-model-backed classifiers, verified
end-to-end against real models in this sandbox. They ended up on **different architectures**,
and that divergence is deliberate, not leftover inconsistency - read on before "fixing" it:

- **Phase 8 (text)** is ONNX/`onnxruntime_go`/CGO-backed: `eliasalbouzidi/distilbert-nsfw-text-
  classifier` (Apache-2.0), exported once via `scripts/export_text_model.py` into a gitignored
  `models/text-nsfw/` directory. `CGO_ENABLED=1` and a C toolchain are required to build this
  project *because of this package* - there's no CGO-free path for the text classifier.
- **Phase 7 (image)** was originally built the same way, against NudeNet v3 (a YOLOv8-style ONNX
  detection export). The user was asked and explicitly declined to fetch NudeNet's AGPLv3-licensed
  weights. Rather than ship an unprovisioned classifier, the image backend was **replaced entirely**
  with GantMan/nsfw_model (MobileNetV2, MIT-licensed), ported from a sibling MIT-licensed project
  by the same author (`privoxy-nsfw-guard`) that embeds the model directly in the binary
  (`internal/classify/image/model.bin`, `//go:embed`) and runs it with a from-scratch **pure-Go**
  inference engine - no ONNX Runtime, no CGO, no download, no licensing decision to make. See the
  Phase 7 notes below for the full history (NudeNet attempt → AGPL decline → GantMan pivot) and why
  direct code reuse from `privoxy-nsfw-guard` was safe and appropriate here.

Net effect: the project as a whole still requires `CGO_ENABLED=1` (for text), but
`internal/classify/image` alone builds and tests fine with `CGO_ENABLED=0` - worth knowing if a
future session wants to reconsider whether project-wide CGO is still justified now that only one
of the two classifiers needs it. This document is written so a new session (human or AI) can
resume without re-deriving context.

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
  - **ML classifiers (NSFW image detection, adult-text detection) use real pretrained models for
    functional parity** (not a pure-Go heuristic drop-in), unconditionally compiled in rather than
    an opt-in build tag. The text classifier uses ONNX Runtime via CGO (the *only* place CGO is
    used in this project); the image classifier is pure Go with its model embedded in the binary -
    see the Phase 7/8 notes below for why they diverged.

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
| 7. Image classification | ✅ Done, verified against real images | `internal/classify/image`: GantMan/nsfw_model (MobileNetV2, MIT-licensed) embedded in the binary (`model.bin`, `//go:embed`), executed by a from-scratch pure-Go inference engine (`nn.go`, ported from `privoxy-nsfw-guard`) - no CGO, no ONNX Runtime, no download. Verified against real onnxruntime output (checked-in fixtures, max diff 0.0178) and real sample photos (nude scores higher than scene) - see the Phase 7 notes below for the full NudeNet→GantMan pivot history |
| 8. Text classifier ML stage | ✅ Done, verified against a real model | `internal/classify/text`: ONNX-backed DistilBERT scorer (`eliasalbouzidi/distilbert-nsfw-text-classifier`, Apache-2.0) replacing the earlier untrained TF-IDF stage - from-scratch Go WordPiece tokenizer, `scripts/export_text_model.py` (via `uv`) exports the real model; run end-to-end against real exported weights in this sandbox with plausible scores (see below) |
| 9. Categories, neighbors/ARP | ✅ Done | `internal/categories` + `internal/neighbors` built and used by the proxy; management-API endpoints live: `GET /api/categories` (real index data), `GET /api/tools/{neighbors,public-ip}`, `POST /api/tools/{youtube,doh,scan}`, `GET /api/logs/export` (CSV + pure-Go XLSX); `webfilter oui update` (real IEEE OUI dataset → `neighbors.Entry.Vendor`) and `webfilter categories update` (real IPFire squidGuard blocklist → `categories/*/domains` + `index.json`) both implemented and verified against live upstream data |
| 10. Hardening, packaging | ✅ Done | Native Windows service (`webfilter service install/start/stop/uninstall/status`), Linux systemd units + installer (`packaging/`), release-archive packaging (`scripts/package-release.sh` + a CI `release` job on `v*` tags) |

`CGO_ENABLED=1 go build ./...`, `go vet ./...`, and `go test ./...` are all green as of this
commit (the first CGO_ENABLED=1 compile this project has ever had in this sandbox - see the
"known gotchas" note in CLAUDE.md's history and the Phase 7/8 sections below; `internal/classify/
image` alone also builds/tests fine with `CGO_ENABLED=0`, since only the text classifier needs
CGO now). `webfilter run`/`webfilter proxy` now perform **real MITM interception and real
filtering** for every addon. The image classifier just works - its model is embedded, so
`image_classifier.enabled` on a policy is the only switch. The text classifier needs
`GlobalSettings.TextClassifierModelPath` pointed at a provisioned model directory to get real ML
scoring on top of the always-on keyword pre-filter; empty means keyword-only (today's default).
Both stay `enabled: false` in `policies/default.json.example` even once provisioned - see the
Phase 7/8 sections below for why enabling stays an explicit operator choice.

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
  origin's cert), and that unsupported `proxy_listen` modes (e.g. `transparent@`) are skipped rather
  than failing the whole engine. Also manually verified end-to-end with a real `curl -x` against
  `https://example.com` (both HTTP and HTTPS/CONNECT) through the built `webfilter proxy` binary.
- **SOCKS5 listener**: a `socks5@host:port` `proxy_listen` entry is now served (not skipped). The
  handshake (`internal/proxy/socks5.go`) implements RFC 1928 CONNECT + RFC 1929 username/password
  auth; BIND/UDP-ASSOCIATE are rejected with reply `0x07`. After the SOCKS reply the connection joins
  the *same* interception path as HTTP CONNECT via the shared `handleTunnel` (blind-splice for
  MITM-excluded hosts, else a first-byte sniff: `0x16` → TLS MITM filtered as https, anything else →
  plaintext HTTP filtered as http — SOCKS5 clients tunnel port-80 traffic directly, unlike CONNECT).
  SOCKS auth reuses `ProxyAuthGate`'s credential store and per-connection authed bookkeeping via the
  `SocksAuthGate` interface. `internal/proxy/socks5_test.go` covers plaintext forwarding, MITM leaf
  issuance, bypass blind-splice, RFC 1929 auth (accept/reject/no-re-challenge), and the
  command-not-supported reply, mirroring the CONNECT-path tests.
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
  exists) and is constructed with the shared `*state.Runtime` directly for that reason. It also
  implements `proxy.SocksAuthGate` (RFC 1929 username/password), the SOCKS analogue of
  `ConnectGate`, reusing the same credential store and per-connection authed map so the SOCKS5
  listener authenticates against the same `proxy_auth_*` settings.
  `management_access.go`'s `dns_request` hook (pseudo-domain DNS answers for dns-mode/transparent/
  WireGuard deployments) has **no equivalent** - this engine doesn't run a DNS listener, and those
  proxy_listen modes are unimplemented (SOCKS5 is now implemented; see the SOCKS5 listener note in
  the verification section above).
  `doh_filter.go` pulled in `github.com/miekg/dns` (new dependency) for RFC 8484 wireformat
  encode/decode + EDNS0/EDE parsing - hand-rolling that wire format wasn't worth it.
  `image_classifier.go` pulled in `github.com/disintegration/imaging` (new dependency, pure Go) for
  Gaussian blur; the checkerboard/block actions and the pixel-dimension gate use only stdlib
  `image`/`image/draw`/`image/color` (the latter mirrors PIL's lazy header-only `Image.open().size`
  read via `image.DecodeConfig`, no full decode).
- **Image classifier (Phase 7, done, verified against real images)**: `internal/classify/image`
  implements `addons.ImageDetector` (`Score(imageBytes) (float64, bool)`) with GantMan/nsfw_model
  (MobileNetV2 1.4@224, MIT-licensed - https://github.com/GantMan/nsfw_model), embedded directly in
  the binary and run by a from-scratch pure-Go inference engine. **No CGO, no ONNX Runtime, no
  model download, nothing to provision** - `image_classifier.enabled` on a policy is the only
  switch. This is a replacement architecture, not the original design - see the pivot history
  below before assuming the text classifier's ONNX/CGO pattern applies here too.

  **Pivot history**: Phase 7 was originally built the same way as Phase 8 - a
  `yalue/onnxruntime_go`-backed detector for NudeNet v3's YOLOv8-style ONNX export, downloaded via
  a `webfilter models download` command. Along the way, reading NudeNet's actual Python inference
  source (`notAI-tech/NudeNet`'s `nudenet/nudenet.py`, `v3` branch) turned up a real bug in that
  implementation - the preprocessing wasn't standard Ultralytics YOLOv8 letterboxing (centered,
  114-gray padding) as originally assumed, but a top-left-anchored, black-filled pad - and that fix
  was made and verified to compile under CGO. But when asked, the user explicitly declined to fetch
  NudeNet's **AGPLv3-licensed** weights (confirmed via GitHub's license API), leaving that backend
  code-complete but unprovisioned. Rather than ship a classifier nobody could actually turn on, the
  user pointed at `C:\Users\yjlio\Documents\test_code\` - a folder of their own sibling projects
  exploring the same problem - and asked for a licensing-clean alternative research pass.

  That research found **GantMan/nsfw_model (MIT-licensed)** used by three sibling projects, and one
  of them - **`privoxy-nsfw-guard`** (`github.com/Yjlion/privoxy-nsfw-guard`, same author, also
  MIT-licensed) - had already solved the whole problem: it embeds the model directly in its binary
  (`model.bin`, fp16-quantized weights + a flattened ONNX op list, ~8.6MB) and executes it with a
  from-scratch pure-Go engine (`nn.go`), verified there against real onnxruntime output (max
  class-probability diff ≤0.018, same argmax). Since it's the user's own already-built-and-verified
  project, the model, engine, and skin-region prefilter (`skinprefilter.go`, from `detect.go`) were
  **ported directly** rather than rebuilt - `model.bin` is a byte-for-byte copy (sha256 verified
  identical at copy time), `nn.go`'s op implementations are unchanged besides type renames
  (`Tensor`→`nnTensor`, `Model`→`nnModel`) to avoid colliding with this package's own `Scores`/
  `detector` types. `scripts/nsfw-model/{convert.py,tflite_to_onnx.py,verify.py}` (also ported) and
  its `README.md` document the conversion pipeline for regenerating `model.bin` from a newer
  GantMan release, should that ever be needed.

  This also **simplified `addons.ImageDetector`**: the interface used to be
  `Detect(imageBytes) ([]Detection, error)` (designed for NudeNet's multi-object detection output,
  checked against a `nsfwLabels` map of 5 exposed classes). GantMan is a whole-image classifier
  with no bounding boxes, so the interface changed to `Score(imageBytes) (float64, bool)` - the
  same shape as `addons.MLScorer` - and the `Detection`/`nsfwLabels` machinery in
  `image_classifier.go` was deleted outright, not just adapted. `GlobalSettings.
  ImageClassifierModelPath` and the `webfilter models download` command (`cmd_models.go`) were
  deleted too - there's nothing left to configure or download for this classifier.

  **Verification**: `go build`/`vet`/`test ./internal/classify/image/...` pass with
  `CGO_ENABLED=0` (no C toolchain needed for this package at all now). The ported
  `TestModelMatchesReference` fixture test (checked-in `testdata/model_fixtures.json`, the same
  onnxruntime cross-check fixtures `privoxy-nsfw-guard` used) passes with max diff 0.0178 - same
  ballpark as the original project, confirming the port is faithful. A new
  `TestScoreRealSampleImages` test feeds real photos (`testdata/nude.jpg`/`scene.jpg`, also ported)
  through `Score()` end-to-end and confirms the nude photo scores higher than the scene photo
  (0.145 vs 0.000 in this run). The full project (`CGO_ENABLED=1`, needed for text) also
  build/vet/tests clean with this change in place.
- **Text classifier ML stage (Phase 8, done, verified against a real model)**:
  `internal/classify/text` replaced its earlier pure-Go TF-IDF/logistic-regression scorer (trained
  only on a tiny tame smoke-test corpus, never a shippable model) with an ONNX-backed export of
  [`eliasalbouzidi/distilbert-nsfw-text-classifier`](https://huggingface.co/eliasalbouzidi/distilbert-nsfw-text-classifier)
  (DistilBERT, Apache-2.0, 190k-example training set, reported F1 0.974). No ONNX export of that
  model exists publicly, so `scripts/export_text_model.py` (Python, run via `uv run --with
  transformers --with "optimum[onnxruntime]" --with torch` - no system Python needed) converts it
  once via HuggingFace `optimum`, deriving a minimal `config.json` (just `max_position_embeddings`,
  `do_lower_case`, `id2label`, and the four special-token ids - not the full HF config) alongside
  `model.onnx`/`vocab.txt`. `internal/classify/text/tokenizer.go` is a from-scratch Go
  implementation of BERT WordPiece tokenization (basic tokenize + greedy longest-match subword
  split against `vocab.txt`) - considered and rejected `github.com/knights-analytics/hugot` (wraps
  onnxruntime_go *and* a Rust-built `tokenizers.a` static lib) since it would add a second native
  toolchain dependency for a well-defined, self-contained algorithm. `model.go`/`session.go` bind
  the ONNX session's inputs by *name* (`input_ids`/`attention_mask`/optionally `token_type_ids`),
  not by fixed position like the image detector does, since an `optimum` export's exact input set
  varies; the "nsfw" output logit index is resolved from the model's own `id2label`, not hardcoded,
  so a future re-export with a different label order can't silently invert the score.

  **Verification**: the WordPiece tokenizer's test fixtures (`tokenizer_test.go`) aren't
  hand-estimated - they were captured directly from the real HF tokenizer via `uv run --with
  transformers -- python -c "..."` against this exact model's `vocab.txt` (this caught a real bug
  during development: `max_input_chars_per_word` was initially assumed to be BERT's commonly-cited
  200, but the live tokenizer confirmed the actual default is 100). `scripts/export_text_model.py`
  was run for real in this sandbox (`uv run` pulled transformers/optimum/torch/onnxruntime
  ephemerally, no system Python) and produced a working `models/text-nsfw/{model.onnx,vocab.txt,
  config.json}`. A throwaway smoke test loading that real model via `text.Load` and scoring sample
  sentences confirmed sane end-to-end behavior: benign sentences ("The weather today is sunny...")
  scored ~0.0001-0.0005, explicit-content sentences scored ~0.9999-1.0000. `CGO_ENABLED=1 go
  build/vet/test ./...` passes.

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
      onnxrt/                      # shared process-global onnxruntime environment init, used by
                                    #   internal/classify/text (ort.InitializeEnvironment is once-
                                    #   per-process; also resolves the shared library next to the
                                    #   running executable, for packaged releases)
      text/                        # Phase 8: ONNX-backed DistilBERT addons.MLScorer (WordPiece
                                    #   tokenizer + onnxruntime_go session), CGO required
      image/                       # Phase 7: addons.ImageDetector, pure Go, no CGO - GantMan/
                                    #   nsfw_model embedded (model.bin) + from-scratch inference (nn.go)
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
  .github/workflows/ci.yml     # build+vet+test (CGO_ENABLED=1 now, mandatory), cross-compile
                                #   matrix (CGO_ENABLED=1 + per-target C cross-compiler), a
                                #   `release` job on `v*` tags (Phase 10)
  .claude/launch.json          # local dev-server config for `mgmt` (used with Claude's preview tools)
  packaging/                   # Phase 10: systemd units (webfilter.service, -proxy, -mgmt),
                                #   install.sh (now also installs libonnxruntime.so next to the
                                #   binary), README.md covering both Linux and Windows deployment
  scripts/
    package-release.sh         # cross-compiles (CGO_ENABLED=1 per target) + bundles all 3 release
                                #   targets, plus the matching onnxruntime shared library, into
                                #   tarballs/zip
    archive.go                 # `//go:build ignore` helper - pure-Go tar.gz/zip writer package-
                                #   release.sh shells out to via `go run`, so packaging doesn't
                                #   depend on a host `tar`/`zip` binary being installed
    export_text_model.py       # Phase 8's one-time HF-to-ONNX conversion script (run via `uv run`,
                                #   see the Phase 8 notes above) - replaced train_text_classifier.go
    nsfw-model/                 # Phase 7's model provenance + regeneration pipeline (convert.py,
                                 #   tflite_to_onnx.py, verify.py, README.md) - not run at build
                                 #   time, model.bin is already committed
```

`internal/block/` from the original plan doesn't exist as a separate package - this port's
block-page rendering instead lives in `internal/proxy/block.go`, no separate package was needed.

## How to build, run, and test

```bash
# CGO_ENABLED=1 and a C toolchain are required now - the text classifier is
# onnxruntime_go-backed. On Windows, MSYS2's mingw64 gcc.exe works (set
# CC=gcc and put it on PATH). The image classifier alone doesn't need this
# (CGO_ENABLED=0 go build ./internal/classify/image/... also works).
CGO_ENABLED=1 go build ./...
go vet ./...
go test ./...            # full suite across every package

go build -o webfilter.exe ./cmd/webfilter   # produce the CLI binary (Windows)
./webfilter.exe mgmt --settings config/settings.json    # management server only
./webfilter.exe proxy --settings config/settings.json   # forward-proxy engine, full MITM + filtering
./webfilter.exe run --settings config/settings.json     # both, in one process

# The image classifier (Phase 7) needs no provisioning - its model is
# embedded in the binary. The text classifier (Phase 8) needs a one-time
# export, since its model ships as PyTorch weights, not ONNX:
uv run --with transformers --with "optimum[onnxruntime]" --with torch \
    scripts/export_text_model.py --out models/text-nsfw   # DistilBERT (Apache-2.0, text)
# then point GlobalSettings.TextClassifierModelPath at the result and
# enable image_classifier/text_classifier on a policy.
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

- `GlobalSettings` has one Go-only optional field, `text_classifier_model_path` (`omitempty`), not
  present in the Python schema — points at a model *directory* (model.onnx+vocab.txt+config.json -
  a breaking change from this project's earlier TF-IDF era, where it pointed at a single JSON
  sidecar; `loadTextScorer` in `runners.go` warns specifically if it sees a stale `.json` path).
  Round-trips harmlessly through the Python original since it doesn't validate unrecognized
  `settings.json` keys. There is no equivalent field for the image classifier - its model is
  embedded in the binary, not configured by path.
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

Both Phase 7 and Phase 8 are now done and verified against real models (see the phase table and
the detailed Phase 7/Phase 8 notes above). What's left is narrower:

1. **Phase 7**: done and verified end-to-end (real onnxruntime cross-check fixtures, real sample
   photos scoring correctly) - no further action needed unless a newer GantMan model release is
   wanted later, in which case `scripts/nsfw-model/README.md` documents the regeneration pipeline.
   Worth double-checking the CI `test` job still passes (it now builds `internal/classify/image`
   without needing the C-toolchain steps that package used to require).
2. **Phase 8**: done and verified end-to-end (real export, real scores on sample sentences) - no
   further action needed unless a different/updated upstream model is desired later, in which case
   `scripts/export_text_model.py --model <other-hf-id>` is the entry point. Watch the CI
   `cross-compile`/`release` jobs' first run with the per-target C cross-compilers
   (`gcc-mingw-w64-x86-64`, `gcc-aarch64-linux-gnu`) these still need for the text classifier - only
   `linux/amd64` (native to the `ubuntu-latest` runner) and `windows/amd64` (this dev sandbox's own
   MSYS2 mingw64) have actually been exercised so far; `linux/arm64` cross-compilation has not been
   run end-to-end anywhere yet.
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
   targets (`windows/amd64`, `linux/amd64`, `linux/arm64`) with `-ldflags` injecting
   `internal/version.{Version,Commit,BuildDate}`, bundles each with `settings.example.json`,
   `default.json.example`, and the relevant `packaging/` files, and archives them - `.tar.gz` for
   Linux, `.zip` for Windows, written via a small pure-Go helper (`scripts/archive.go`, `//go:build
   ignore`) rather than shelling out to a host `tar`/`zip` binary. As of the Phase 8 ONNX rework,
   this now also builds with `CGO_ENABLED=1` and a per-target C cross-compiler
   (`x86_64-w64-mingw32-gcc`/native `gcc`/`aarch64-linux-gnu-gcc`), and downloads + bundles the
   matching prebuilt `onnxruntime` shared library (pinned version, from `microsoft/onnxruntime`'s
   own GitHub releases) into each archive alongside the binary, for the text classifier - see that
   script's header comment. The image classifier needs none of this (model embedded, pure Go).
   `.github/workflows/ci.yml`'s `release` job runs this script and attaches the archives via
   `softprops/action-gh-release@v2` whenever a `v*` tag is pushed. **Verification**: the original
   `CGO_ENABLED=0` version of this script was run and inventoried end-to-end in an earlier session;
   the updated CGO/onnxruntime-bundling version has not yet had a full three-target run in this
   session (this sandbox can only natively produce the `windows/amd64` target - see the script's
   own header comment on why linux cross-compilation needs CI) - worth watching the first real tag
   push after this change lands.

All 11 phases now have real, tested code behind them. Both Phase 7 (image) and Phase 8 (text) are
verified end-to-end against real models - see the Phase 7/8 notes above for the full story,
including the mid-session pivot on Phase 7 from NudeNet (AGPLv3, declined) to GantMan/nsfw_model
(MIT, pure Go, embedded) after a licensing-driven research detour into the user's own sibling
projects. Remaining work: watch the updated CI cross-compile/release jobs on their first real run
with the new C cross-compilers (still needed for the text classifier only), optionally wire the
mgmt API's `/api/tools/scan` into the now-real classifiers, or general hardening/bug-fixing as
issues surface in real use.
