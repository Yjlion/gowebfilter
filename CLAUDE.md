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

```bash
go build ./...
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
- `internal/classify/{text,image}/` — optional ML stages (pure-Go TF-IDF
  text classifier; ONNX-backed image classifier behind `-tags onnx`, CGO)
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
