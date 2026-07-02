# gowebfilter

A single-binary, policy-based web-filtering proxy for a household or small
office network: MITM-intercepts HTTP/HTTPS traffic, applies per-client
(IP/MAC/CIDR) policies, and ships a browser-based management UI for
configuring everything. It's a from-scratch Go port of
[mitmproxy-web-filter](https://github.com/Yjlion/mitmproxy-web-filter),
aimed at replacing a Python + mitmproxy runtime with one static executable.

> **⚠️ Vibe-coded disclaimer.** This project was built almost entirely
> through AI-assisted ("vibe coding") sessions with Claude Code, with a
> human reviewing direction and testing rather than writing most of the
> code by hand. It has real test coverage and has been exercised against
> live traffic, but it has **not** had an independent human security audit.
> If you're going to point this at real household/office traffic — and
> especially if you're going to trust its MITM certificate authority or
> expose its management UI beyond localhost — read the code for the parts
> you care about (TLS interception, auth, policy matching) before relying
> on it. Treat it as a personal/homelab project, not audited security
> software.

## What it does

- **TLS-intercepting forward proxy**: generates its own CA, issues per-host
  leaf certificates on the fly, and filters decrypted HTTP/HTTPS traffic
  through an ordered addon pipeline (mirrors mitmproxy's addon model).
- **Per-client policy routing**: policies match by MAC address, exact IP,
  CIDR range, or a catch-all default — the same tiered matching as the
  Python original.
- **Filtering addons**: URL allow/blacklist with category blocklists
  (ads/porn/malware/etc.), SafeSearch enforcement across Google/Bing/
  DuckDuckGo/Yahoo/YouTube, YouTube channel filtering, DNS-over-HTTPS
  blocking, QUIC blocking (to force fallback to inspectable HTTP), an
  ONNX-backed adult-text classifier (a DistilBERT export), and an
  ONNX-backed NSFW image classifier (NudeNet v3) — both real pretrained
  models, downloaded/exported separately (see Models below), not bundled
  in the binary or this repo.
- **Management UI**: policy editor, live logs/analytics, PAC file
  generation, neighbor/ARP scanning for picking devices by MAC, category
  list management — served from the same binary, no separate install.
- **Single binary + one shared library**: no Python runtime or virtualenv
  to bundle; cross-compiles for Windows and Linux (x86_64/arm64). Requires
  `CGO_ENABLED=1` and a matching C toolchain to build (the ONNX-backed
  classifiers are onnxruntime_go/CGO-based — there's no CGO-free build
  variant), and the onnxruntime shared library alongside the binary at
  runtime (bundled in release archives — see
  [packaging/README.md](packaging/README.md)).

## Quick start

Building requires `CGO_ENABLED=1` and a C toolchain (the ONNX-backed
classifiers need it — see Building and testing below). On Windows, install
[MSYS2](https://www.msys2.org/) and add its mingw64 `gcc.exe` to `PATH`.

```bash
go build -o webfilter.exe ./cmd/webfilter   # or `webfilter` on Linux

cp config/settings.example.json config/settings.json
cp policies/default.json.example policies/default.json

./webfilter.exe run --settings config/settings.json
```

Then open `http://127.0.0.1:8000` for the management UI, and point clients
at `127.0.0.1:8080` as their HTTP(S) proxy (import `certs/ca.crt` into the
client's trust store to avoid TLS warnings once MITM starts intercepting).

`run` starts both the proxy engine and the management server in one
process. `webfilter proxy` and `webfilter mgmt` run them standalone if you
want process isolation.

## Building and testing

```bash
CGO_ENABLED=1 go build ./...   # requires a C toolchain - see Quick start
go vet ./...
go test ./...
```

See [HANDOFF.md](HANDOFF.md) for the full phase-by-phase build history,
what's verified vs. not, and architecture notes for anyone (human or AI)
picking this project back up. See [packaging/README.md](packaging/README.md)
for running it as a Windows service or Linux systemd unit.

## Models

Neither ML classifier ships a model in this repo or in `go build`'s output —
they're provisioned separately, once, into a gitignored `models/` directory:

- **Image (NudeNet v3)**: `webfilter models download` fetches
  `320n.onnx` plus its label file into `models/image-nsfw/`. **NudeNet's
  weights are AGPLv3-licensed** by [notAI-tech](https://github.com/notAI-tech/NudeNet)
  — a separate license from this project's own; the command prints the
  notice and a `LICENSE-NOTICE.txt` before downloading. Review the AGPLv3's
  terms before enabling image classification in production.
- **Text (DistilBERT NSFW classifier)**: no ONNX export of
  [eliasalbouzidi/distilbert-nsfw-text-classifier](https://huggingface.co/eliasalbouzidi/distilbert-nsfw-text-classifier)
  (Apache-2.0) exists publicly, so it's converted once via
  [uv](https://docs.astral.sh/uv/) (no system Python needed):

  ```bash
  uv run --with transformers --with "optimum[onnxruntime]" --with torch \
      scripts/export_text_model.py --out models/text-nsfw
  ```

Then point `image_classifier_model_path` / `text_classifier_model_path` in
`settings.json` at the resulting paths (see `config/settings.example.json`)
and enable `image_classifier`/`text_classifier` on the policies that should
use them — both default to disabled per-policy even once models are
provisioned, since NSFW classification false positives (blurring a
legitimate medical or news image/page) have real cost and this is an
explicit opt-in, not an automatic default.

## Configuration

Config lives entirely on disk, matching the Python original's layout:

- `config/settings.json` — global settings (listen addresses, mgmt port,
  auth, directories). Requires a restart to pick up changes.
- `policies/*.json` — per-client policies. Hot-reloaded; edit via the UI
  or the file directly.
- `certs/` — generated CA + leaf certificate cache.
- `categories/` — domain-list blocklists (`webfilter categories update`
  refreshes these from the IPFire squidGuard blocklist).
- `models/` — ML classifier models (`webfilter models download` /
  `scripts/export_text_model.py` populate this — see Models above).
- `logs/webfilter.db` — SQLite request/block log, browsable from the UI.

None of the above are committed to this repo (see `.gitignore`) — they're
runtime state, generated or copied from the shipped `.example` templates on
first run.
