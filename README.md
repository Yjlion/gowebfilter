# gowebfilter

A single-binary, policy-based web-filtering proxy for a household or small
office network: MITM-intercepts HTTP/HTTPS traffic, applies per-client
(IP/MAC/CIDR) policies, and ships a browser-based management UI for
configuring everything. It is a from-scratch Go port of
[mitmproxy-web-filter](https://github.com/Yjlion/mitmproxy-web-filter),
aimed at replacing a Python + mitmproxy runtime with one static executable.

> **Vibe-coded disclaimer.** This project was built almost entirely through
> AI-assisted sessions with a human reviewing direction and testing rather
> than writing most of the code by hand. It has real test coverage and has
> been exercised against live traffic, but it has not had an independent
> human security audit. Treat it as a personal/homelab project, not audited
> security software.

## What it does

- **TLS-intercepting forward proxy**: generates its own CA, issues per-host
  leaf certificates on the fly, and filters decrypted HTTP/HTTPS traffic
  through an ordered addon pipeline.
- **Per-client policy routing**: policies match by MAC address, exact IP,
  CIDR range, or a catch-all default, using the same tiered matching as the
  Python original.
- **Filtering addons**: URL allow/blacklist with category blocklists,
  SafeSearch enforcement, YouTube channel filtering, DNS-over-HTTPS
  blocking, QUIC blocking, an embedded pure-Go Bayesian adult-text
  classifier, and a pure-Go embedded NSFW image classifier.
- **Management UI**: policy editor, live logs/analytics, PAC file
  generation, neighbor/ARP scanning, and category list management.
- **Native desktop UI**: `webfilter gui` opens a native window
  (github.com/gogpu/ui — pure Go, GPU-rendered, still no CGO) covering the
  dashboard, policies, logs, and settings, with the web UI one click away
  for everything else.
- **Single binary**: no Python runtime, virtualenv, native ML runtime, or
  sidecar DLL to bundle; cross-compiles for Windows and Linux
  (x86_64/arm64) with `CGO_ENABLED=0`.

## Quick start

Building does not require CGO or a C toolchain.

```bash
go build -o webfilter.exe ./cmd/webfilter   # or `webfilter` on Linux

cp config/settings.example.json config/settings.json
cp policies/default.json.example policies/default.json

./webfilter.exe run --settings config/settings.json
```

Then open `http://127.0.0.1:8000` for the management UI, and point clients
at `127.0.0.1:8080` as their HTTP(S) proxy. Import `certs/ca.crt` into the
client's trust store to avoid TLS warnings once MITM starts intercepting.

`run` starts both the proxy engine and the management server in one
process. `webfilter proxy` and `webfilter mgmt` run them standalone if you
want process isolation.

`webfilter gui` opens the native desktop window instead: if nothing is
serving the management port it hosts the proxy + management server itself
(closing the window then stops filtering); if a server is already running
(`run`, the tray, or a service) it attaches to it and closing the window
changes nothing. Headless servers are unaffected — the GUI toolkit is
compiled in but only touches a display when you actually run `gui` (on
Linux that command needs X11/Wayland at runtime; building does not).

## Building and testing

```bash
CGO_ENABLED=0 go build ./...
go vet ./...
go test ./...
```

See [HANDOFF.md](HANDOFF.md) for the full phase-by-phase build history,
what is verified vs. not, and architecture notes for anyone picking this
project back up. See [packaging/README.md](packaging/README.md) for running
it as a Windows service or Linux systemd unit.

## Models

- **Image (GantMan/nsfw_model)**: no setup needed. The model
  ([MobileNetV2, MIT-licensed](https://github.com/GantMan/nsfw_model)) is
  embedded directly in the binary (`internal/classify/image/model.bin`,
  ~8.6MB) and run by a from-scratch pure-Go inference engine. See
  `scripts/nsfw-model/README.md` for provenance and regeneration notes.
- **Text (embedded Bayesian scorer)**: no setup needed. A compact
  adult-text feature table is embedded in the binary and scored with a
  pure-Go Naive Bayes classifier. The seed vocabulary is curated from
  LDNOOBW's English list concepts with CC-BY-4.0 attribution; see
  `internal/classify/textbayes/NOTICE`.

Enable `image_classifier`/`text_classifier` on the policies that should use
them. Both default to disabled per-policy because NSFW classification false
positives have real cost and should be an explicit opt-in.

## Configuration

Config lives entirely on disk, matching the Python original's layout:

- `config/settings.json` - global settings. Requires a restart to pick up
  changes.
- `policies/*.json` - per-client policies. Hot-reloaded; edit via the UI or
  the file directly.
- `certs/` - generated CA + leaf certificate cache.
- `categories/` - domain-list blocklists refreshed by
  `webfilter categories update`.
- `logs/webfilter.db` - SQLite request/block log, browsable from the UI.

Runtime state is generated or copied from the shipped `.example` templates
and is not committed to the repo.
