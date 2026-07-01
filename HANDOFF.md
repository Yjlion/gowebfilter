# Handoff: Go port of mitmproxy-web-filter

Status as of this commit: **Phases 0–3 of 11 complete and tested.** This document is
written so a new session (human or AI) can resume without re-deriving context.

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
| 4. Minimal proxy engine | ⬜ Not started | Plain HTTP + CONNECT blind-splice passthrough |
| 5. MITM + pipeline skeleton | ⬜ Not started | Wire cert issuance into CONNECT, addon chain (no-op stubs) |
| 6. Filtering addons | ⬜ Not started | All 12 addons ported from Python |
| 7. ONNX image classification | ⬜ Not started | NSFW detection via onnxruntime_go |
| 8. Text classifier ML stage | ⬜ Not started | Offline-trained model + Go inference |
| 9. Categories, neighbors/ARP | ⬜ Not started | Category blocklists, MAC resolution, backup/tools endpoints |
| 10. Hardening, packaging | ⬜ Not started | Service install, release archives, docs |

`go build ./...`, `go vet ./...`, and `go test ./...` are all green as of this commit. There is no
`main`/proxy engine yet — `webfilter run` currently starts *only* the management server (logs a
warning that the proxy engine isn't implemented) so the UI is usable for Phase 1–3 verification.

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
- **MITM proxy engine (Phase 4/5, not yet built)**: hand-rolled `net.Listener` +
  `crypto/tls.Config.GetCertificate`, deliberately **not** `elazarl/goproxy` — goproxy's
  request-handler model and built-in cert store would fight the exact on-disk `certs/` persistence
  and the purpose-built `FlowContext` metadata-flag pipeline (`URLAllowed`, `MitmPassthrough`,
  `WFAction`, `WFComponent`, `Policy`) the whole addon chain is designed around. The CA/leaf-cert
  machinery this depends on (`internal/certs`) is already built and tested.
- **Addon pipeline (Phase 5/6, not yet built)**: fixed execution order mirroring the Python
  original's `proxy/main.py` exactly:
  - Request hooks: `ManagementAccess` → `ProxyAuthGate` → `PolicyRouter` → `MitmControl` →
    `UrlFilter` → `DohFilter` → `SafeSearch`
  - Response hooks: `QuicBlocker` → `YouTubeFilter` → `TextClassifier` → `ImageClassifier` →
    `RequestLogger`
  - Error hook: `RequestLogger` (dedup-guarded)
  - Each addon checks `URLAllowed`/`MitmPassthrough` at its own top as a literal early-return guard
    (not a generic pipeline-level skip) — different addons key off different combinations of these
    flags, so this is ported faithfully rather than abstracted away.
- **ONNX/CGO packaging (Phase 7, not yet built)**: `yalue/onnxruntime_go` dynamically loads a
  companion `onnxruntime.dll`/`libonnxruntime.so` shipped alongside the executable (not statically
  linked — this is that library's normal usage pattern). A `-tags noonnx` build variant stubs the
  image classifier as a pass-through for environments that can't ship CGO binaries. Recommended
  cross-compiler for the CGO build: `zig cc` (one Zig install cross-compiles to
  windows/amd64+linux/amd64+linux/arm64 from a single Linux CI box).
- **Text classifier ML stage (Phase 8, not yet built)**: the Python original's scikit-learn joblib
  model can't be loaded in Go. Plan is to retrain a small TF-IDF + logistic-regression model
  offline, export weights as a JSON/gob sidecar, and implement both an ONNX-backed scorer (reusing
  the same runtime as the image classifier) and a pure-Go fallback scorer for `-tags noonnx`
  builds. **This will not be pixel-perfect parity** with the original model's decision boundary —
  expect to recalibrate `text_classifier.threshold` empirically; a `webfilter classify text-eval`
  dev tool was planned for this.

## Go package layout (as built so far)

```
gowebfilter/
  cmd/webfilter/            # cobra CLI: main.go, cmd_run.go, cmd_proxy.go, cmd_mgmt.go,
                             #   cmd_categories.go, cmd_oui.go, flags.go, runners.go
  internal/
    macutil/                 # MAC address normalization (shared by models + future neighbors pkg)
    models/                   # Policy, GlobalSettings, all sub-configs, proxy_listen parser
    config/                    # settings.json + policies/*.json load/save, fsnotify watch wrapper
    logstore/                  # modernc.org/sqlite: schema, single-writer, analytics, prune, export
    pwhash/                     # PBKDF2-SHA256 (mgmt password + future proxy-auth password)
    certs/                       # CA generation/import/export, per-host leaf cert issuance + cache
    mgmtapi/                      # chi router, auth, all current routes, PAC generator, static UI
  ui/                          # management UI copied verbatim from the Python repo + embed.go
  config/settings.example.json # shipped template (matches the Python original's)
  policies/default.json.example # shipped template
  .github/workflows/ci.yml     # build+vet+test, cross-compile matrix (pure-Go, CGO_ENABLED=0)
  .claude/launch.json          # local dev-server config for `mgmt` (used with Claude's preview tools)
```

Directories **not yet created** (per the original plan, for the phases still to come):
`internal/proxy/` (engine, MITM, addon pipeline, `addons/*`), `internal/block/` (block-page
rendering, 7-language labels), `internal/classify/text/` and `internal/classify/image/`,
`internal/categories/`, `internal/neighbors/`.

## How to build, run, and test

```bash
go build ./...          # build everything
go vet ./...
go test ./...            # full suite, currently ~30 tests across 6 packages

go build -o webfilter.exe ./cmd/webfilter   # produce the CLI binary (Windows)
./webfilter.exe mgmt --settings config/settings.json   # management server only (proxy not built yet)
```

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
- HTTP/2 over MITM'd connections is planned to be **out of scope for v1** (advertise only
  `http/1.1` via ALPN on intercepted connections) once Phase 5 builds the actual TLS termination —
  this avoids a large amount of HPACK/stream-multiplexing complexity interacting with the addon
  pipeline. Real behavioral difference from Python mitmproxy (which supports h2); flag to the user
  if it becomes a problem in practice.

## Suggested next step

Phase 4: minimal proxy engine — plain HTTP forward-proxy + CONNECT blind-splice passthrough for
*all* hosts (no MITM, no addons yet). Verify by pointing a real browser's proxy settings at
`webfilter proxy` and confirming both HTTP and HTTPS browsing work with zero filtering. This proves
the socket-handling foundation before layering MITM (Phase 5) and the addon chain (Phase 6) on top.
`internal/certs` (CA + leaf issuance) is already built and tested and ready to be wired in during
Phase 5.
