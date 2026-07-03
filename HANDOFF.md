# Handoff: Go port of mitmproxy-web-filter

Status: the project is a single-binary Go web-filtering proxy with
management UI, MITM interception, policy routing, filtering addons, and
embedded NSFW classifiers. Both classifiers are pure Go and embedded:

- `internal/classify/image`: GantMan/nsfw_model (MobileNetV2,
  MIT-licensed), embedded as `model.bin` and executed by the local pure-Go
  inference engine.
- `internal/classify/textbayes`: compact embedded Bayesian adult-text
  scorer. It implements `addons.MLScorer`, needs no model directory, and
  does not use ONNX Runtime, GoMLX, Python, CGO, or a sidecar DLL.

`CGO_ENABLED=0 go build ./...`, `go vet ./...`, and `go test ./...` are the
expected verification commands after changes.

## Architecture

- One binary: `webfilter run|proxy|mgmt|categories update|oui update|version`.
- `run` starts the proxy engine and management server together; `proxy` and
  `mgmt` remain available for process isolation.
- `proxy_listen` supports both `regular@host:port` and `socks5@host:port`.
  Unsupported modes are skipped with a warning.
- SOCKS5 support lives in `internal/proxy/socks5.go`. It implements CONNECT,
  supports no-auth and username/password auth through the existing
  `ProxyAuthGate`, rejects BIND/UDP-ASSOCIATE, then joins the same tunnel
  path as HTTP CONNECT. HTTPS is raw-spliced when MITM is unavailable or
  bypassed, and MITM-filtered when a runtime CA is available.
- Config and state live on disk: `config/settings.json`, `policies/*.json`,
  `certs/`, `categories/`, `logs/webfilter.db`, and `data/`.
- Settings changes need a restart. Policy changes hot-reload.
- The management UI is served from embedded `ui/` files. Do not switch it
  back to `http.FileServer`/`FileServerFS`; that caused an index redirect
  loop.

## Proxy Pipeline

`cmd/webfilter/runners.go` wires addons in fixed order:

`ManagementAccess -> ProxyAuthGate -> PolicyRouter -> MitmControl ->
UrlFilter -> QuicBlocker -> DohFilter -> SafeSearch -> YouTubeFilter ->
TextClassifier -> ImageClassifier -> RequestLogger`

Order matters. Request hooks still run after an earlier hook sets
`fc.Response`; only the upstream fetch is skipped.

## Classifiers

- Text classification is opt-in per policy through `text_classifier.enabled`.
  The addon keeps the high-confidence keyword prefilter and then uses the
  embedded Bayesian scorer for lower-keyword adult text. The policy
  `text_classifier.threshold` still controls blocking sensitivity.
- Tiny HTML pages with multiple high-confidence adult keyword hits block
  immediately; the 100-character floor only protects weak/ambiguous text from
  Bayesian scoring noise.
- `text_classifier_model_path` is deprecated and ignored. It remains in the
  settings struct only for backward-compatible JSON round trips.
- The Bayesian scorer's seed vocabulary is curated from LDNOOBW English list
  concepts with CC-BY-4.0 attribution; see
  `internal/classify/textbayes/NOTICE`. e2guardian and Redwood lists were
  treated as references only, not embedded data.
- Image classification is opt-in per policy through `image_classifier.enabled`
  and uses the embedded MobileNetV2 model. The image addon also scans inline
  `data:image/...` URIs in HTML/CSS/JS/JSON bodies.

## Known Gotchas

- Policy selection is source based, first match wins, tiered
  MAC -> exact IP -> CIDR -> catch-all.
- Blocked responses return HTTP 200 with a block-page body. Verify filtering
  through request/block logs, not status code alone.
- Response bodies reaching addons are identity-encoded because the engine
  strips the client's `Accept-Encoding` before upstream fetch.
- SafeSearch matching must account for engines where AI/images/videos tabs
  share the normal search hostname.
- Google image thumbnails use sharded hosts such as
  `encrypted-tbn0.gstatic.com` through at least `encrypted-tbn3.gstatic.com`.
- Tests that construct config-backed services directly must use absolute
  temp paths for `cert_dir`, `policies_dir`, and `logs_dir`.
- Local `main` may lag GitHub because fixes have been landing through PRs.
  Before publishing, fetch `origin/main` and reconcile with it so newer
  diagnostics, OUI, PAC, SOCKS, or policy-editor fixes are not dropped.

## Verification Notes

Useful focused checks after classifier or wiring changes:

```bash
CGO_ENABLED=0 go test ./internal/classify/textbayes ./internal/proxy/addons ./cmd/webfilter
CGO_ENABLED=0 go test ./internal/proxy
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -o webfilter.exe ./cmd/webfilter
```

When testing a live instance, inspect:

```bash
curl -s http://127.0.0.1:8000/api/policies
curl -s "http://127.0.0.1:8000/api/logs?kind=requests&limit=20"
curl -s "http://127.0.0.1:8000/api/logs?kind=blocks&limit=20"
```
