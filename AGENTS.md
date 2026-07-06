# AGENTS.md

Guidance for Codex or any AI agent working in this repo.

## What this is

A from-scratch Go port of `mitmproxy-web-filter` (Python/mitmproxy): a
MITM-intercepting forward proxy with per-client filtering policies and a
browser-based management UI, all in one static binary. Full history,
architecture rationale, and what is verified vs. not lives in
[HANDOFF.md](HANDOFF.md); read that before structural changes.

This project was built primarily through AI-assisted sessions. Do not
assume behavior was deliberately hand-verified against real-world traffic
unless HANDOFF.md or a test explicitly says so.

## Build / test / run

Both classifiers are embedded and pure Go. The project should build with
`CGO_ENABLED=0`; no ONNX Runtime DLL, GoMLX backend, Python environment, or
C compiler is required for normal builds.

```bash
CGO_ENABLED=0 go build ./...
go vet ./...
go test ./...

go build -o webfilter.exe ./cmd/webfilter
./webfilter.exe run --settings config/settings.json
```

`config/settings.json` and `policies/*.json` are gitignored runtime state.
Copy from `config/settings.example.json` / `policies/default.json.example`
for local dev. They persist to disk; the mgmt API's
`PUT /api/policies/{name}` writes straight through to
`policies/{name}.json`.

## Layout

- `cmd/webfilter/` - cobra CLI (`run`/`proxy`/`mgmt`/`categories update`/`oui update`)
- `internal/app/` - shared engine wiring (`BuildProxyEngine`, classifier loaders,
  `EnsureTunSocksListener`, `ServeMgmt`); the fixed addon pipeline order is
  single-sourced here and reused by both `cmd/webfilter` and `mobile/`
- `internal/models/` - `Policy`/`GlobalSettings` structs + JSON schema
- `internal/proxy/` - MITM engine, pipeline, block-page rendering
- `internal/proxy/state/` - hot-reloaded settings/policies and policy routing
- `internal/proxy/addons/` - filtering addons, wired in fixed order in `internal/app/engine.go`
- `internal/mgmtapi/` - chi router, REST API, embedded UI static serving
- `internal/classify/textbayes/` - embedded pure-Go Bayesian adult-text scorer
- `internal/classify/image/` - embedded pure-Go GantMan/nsfw_model image classifier
- `mobile/` - gomobile-bound Android entry point (`Start`/`Stop`/`Status`/…);
  drives tun2socks from the VpnService `fd://` TUN. Build with
  `gomobile bind -target=android/arm64,android/arm -androidapi 26 -o android/app/libs/webfilter.aar ./mobile`
- `android/` - Kotlin/Gradle Android app (VpnService, WebView mgmt UI, per-app
  filtering, CA install flow) consuming the gomobile AAR
- `ui/` - management web UI copied from the Python original; Alpine.js and
  qrcodejs are vendored (`ui/alpine.min.js`, `ui/qrcode.min.js`) for offline use

## Known gotchas

- Policy selection is by source IP/MAC, first match wins, tiered
  MAC -> exact-IP -> CIDR -> catch-all. Check `GET /api/policies` before
  assuming the `default` policy applies.
- Blocked responses return HTTP 200 with a block-page body, not 4xx. Check
  `GET /api/logs?kind=requests` or `?kind=blocks` to verify filtering.
- SafeSearch engine matching is host-and-path/param scoped for engines
  whose AI/images/videos tabs share a domain with regular search.
- Google shards image-CDN thumbnail hosts (`encrypted-tbn0` through at
  least `encrypted-tbn3.gstatic.com`); use prefix/suffix matching for that
  hostname family.
- Response bodies reaching addons are identity-encoded. The engine strips
  the client's `Accept-Encoding` before the upstream fetch so content
  inspectors do not scan compressed bytes.
- NSFW images are also embedded as `data:image/...` URIs inside HTML/CSS/JS
  and JSON; image classifier inline scanning handles those.
- Settings changes need a restart; policy changes hot-reload.
- Do not reintroduce `http.FileServer`/`FileServerFS` for the UI static
  path; it caused a `/` <-> `/index.html` redirect loop.
- Test helpers constructing a `Server`/`CA`/`PolicyStore`/log store directly
  must seed absolute temp-dir paths for `cert_dir`/`policies_dir`/`logs_dir`.
- The text classifier has no model path anymore. `text_classifier_model_path`
  is deprecated and ignored for backward-compatible settings round trips.

## Live testing

Verify behavior via the mgmt API and request/block logs, not just curl
status codes:

```bash
curl -s http://127.0.0.1:8000/api/policies
curl -s http://127.0.0.1:8000/api/policies/<name>
curl -s -X PUT http://127.0.0.1:8000/api/policies/<name> -d @updated.json
curl -s "http://127.0.0.1:8000/api/logs?kind=requests&limit=20"
curl -s "http://127.0.0.1:8000/api/logs?kind=blocks&limit=20"
```

If you temporarily change a live policy for testing, restore it before
finishing.
