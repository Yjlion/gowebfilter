# CLAUDE.md

Guidance for Claude Code (or any AI agent) working in this repo.

## What this is

A from-scratch Go port of `mitmproxy-web-filter` (Python/mitmproxy): a
MITM-intercepting forward proxy with per-client filtering policies and a
browser-based management UI, all in one static binary. Full history,
architecture rationale, and what's verified-vs-not lives in
[HANDOFF.md](HANDOFF.md) — read that before making structural changes; it's
long but it's the authoritative "why" behind every design decision here.
This file is the short version for day-to-day work.

**This project was built primarily through AI-assisted sessions** (see the
disclaimer in [README.md](README.md)). Don't assume any given piece of
behavior was deliberately hand-verified against real-world traffic unless
HANDOFF.md or a test explicitly says so.

## Build / test / run

The text classifier (`internal/classify/text`) is ONNX/`onnxruntime_go`-
backed, so **`CGO_ENABLED=1` and a C toolchain are required for every
build** — there is no CGO-free build variant anymore. On Windows, MSYS2's
mingw64 `gcc.exe` works; set `CC`/`PATH` accordingly. The image classifier
(`internal/classify/image`) is pure Go with an embedded model — it doesn't
need CGO on its own, but the project as a whole still does, for text.

```bash
CGO_ENABLED=1 go build ./...
go vet ./...
go test ./...

go build -o webfilter.exe ./cmd/webfilter   # produce the CLI binary
./webfilter.exe run --settings config/settings.json   # proxy (:8080) + mgmt UI (:8000) in one process
```

`config/settings.json` and `policies/*.json` are gitignored runtime state —
copy from `config/settings.example.json` / `policies/default.json.example`
for local dev. They persist to disk; the mgmt API's `PUT /api/policies/{name}`
writes straight through to `policies/{name}.json`.

## Layout

- `cmd/webfilter/` — cobra CLI (`run`/`proxy`/`mgmt`/`categories update`/`oui update`)
- `internal/models/` — `Policy`/`GlobalSettings` structs + JSON schema (custom
  `UnmarshalJSON` per sub-config for defaults + legacy-schema migration —
  see `SafeSearchConfig`'s flat-to-`engines`-map migration as the pattern)
- `internal/proxy/` — the MITM engine (`engine.go`), pipeline (`FlowContext`,
  ordered `[]Addon`), block-page rendering
  - `internal/proxy/state/` — `Runtime`: hot-reloaded settings/policies,
    `GetPolicy(clientIP)` tiered MAC→IP→CIDR→catch-all matching
  - `internal/proxy/addons/` — all filtering addons, one file each, wired in
    `cmd/webfilter/runners.go`'s `buildProxyEngine` in a **fixed pipeline
    order** that matters (mirrors the Python original's addon order)
- `internal/mgmtapi/` — chi router, REST API, embedded UI static serving
- `internal/classify/{text,image}/` — the two NSFW ML classifiers:
  - `text/` — ONNX/`onnxruntime_go`-backed (CGO required), a
    WordPiece-tokenized DistilBERT export
    (`eliasalbouzidi/distilbert-nsfw-text-classifier`, Apache-2.0). Doesn't
    ship a model in-repo; `scripts/export_text_model.py` (via `uv`)
    provisions it into a gitignored `models/text-nsfw/` dir.
    `internal/classify/onnxrt/` holds the process-global onnxruntime
    environment init.
  - `image/` — pure Go, no CGO: GantMan/nsfw_model (MobileNetV2,
    MIT-licensed) embedded directly in the binary as `model.bin`
    (`//go:embed`), executed by a from-scratch pure-Go inference engine
    (`nn.go`). Ported from the same author's
    [privoxy-nsfw-guard](https://github.com/Yjlion/privoxy-nsfw-guard) —
    see `scripts/nsfw-model/README.md` for provenance/regeneration. Works
    immediately after `go build`, no setup, no download, no licensing
    decision to make.
- `ui/` — management web UI, copied verbatim from the Python original

## Known gotchas (don't rediscover these the hard way)

- **Policy selection is by source IP/MAC, first match wins**, tiered
  MAC→exact-IP→CIDR→catch-all. A policy with `source_ips` set takes
  precedence over the catch-all `default` policy for matching clients —
  when testing against the live proxy, check `GET /api/policies` to see
  which named policy actually applies to your test client before assuming
  `default` is what's active.
- **Blocked responses return HTTP 200** with a block-page body, not 4xx —
  don't use the HTTP status code alone to tell whether a request was
  filtered. Check `GET /api/logs?kind=requests` (`action`: `ok`/`modified`/
  `blocked`, `component`) or `?kind=blocks` (includes `reason`) instead.
- **SafeSearch engine matching is host*and*-path/param scoped, not just
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
- `http.FileServer`/`FileServerFS` must not be reintroduced for the UI
  static path — it causes a `/` ↔ `/index.html` redirect loop with this
  UI's own navigation. See `internal/mgmtapi/static.go` and
  `TestIndexDoesNotRedirectLoop`.
- Test helpers that construct a `Server`/`CA`/`PolicyStore`/log store
  directly must seed **absolute** temp-dir paths for `cert_dir`/
  `policies_dir`/`logs_dir` — the documented relative defaults (`./certs`
  etc.) resolve against the test process's working directory, not the
  settings file's location.
- The image classifier's model (`internal/classify/image/model.bin`) is
  **committed to git and embedded via `go:embed`** — unlike the text
  classifier, there's no separate provisioning step, no model path in
  settings, and nothing to download. `image_classifier.enabled` alone
  (per-policy) controls whether it runs.
- The text classifier's model directory (`models/text-nsfw/`) is
  gitignored and not present in a fresh checkout — `text_classifier` stays
  `enabled: false` in `policies/default.json.example` even though the
  backend is always compiled in, so a fresh install fails open
  (keyword-only) rather than silently doing nothing once a model path is
  configured but the directory doesn't exist yet.

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
```

If you temporarily change a live policy for testing, restore it to what you
found before finishing up.
