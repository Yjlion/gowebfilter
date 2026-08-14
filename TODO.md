# TODO

## Recommended Features

- [x] Policy test/simulator API (`POST /api/tools/policy-simulate`: policy
  selection, schedule status, URL allow/block/category results, addon hints),
  surfaced as the Policy Simulator card on the Tools page.
  - [ ] Later: deeper SafeSearch/YouTube simulation (the addon hints are still
    "would_modify"-level rather than an actual rewritten URL).
- [x] Classifier health checks (`GET /api/tools/classifier-health`: text ML
  readiness vs keyword-only fallback, embedded image classifier availability),
  surfaced as the Classifier Health card on the Tools page.
- [x] Scheduled policy modes: schedule evaluation hardened, including
  overnight windows (`internal/models/schedule.go`), with within-tier
  "actively scheduled beats unscheduled" precedence.
  - [ ] Later: add UI presets for school hours, bedtime, weekends, and
    temporary unlocks.
- [x] Policy change audit log (`GET /api/logs?kind=policy_changes`, always
  on).
- [ ] Per-client live activity dashboard.
- [ ] "Why was this blocked?" endpoint and richer block-page explanation.
- [ ] Policy import/export and validation command.
  - `internal/mgmtapi/routes_backup.go` is currently an empty stub — a
    config+policies backup/restore endpoint would fit there.
- [ ] Temporary allow/deny from the block page.
- [ ] DNS/category cache visibility in the management UI.

## Platform / proxy gaps

- [ ] Settings hot-reload. Policies hot-reload today, but `PUT
  /api/settings` requires a restart of `webfilter run` to take effect.
- [ ] macOS support: no macOS target in `scripts/package-release.sh`, and
  tun2socks route setup is not wired on macOS (see `packaging/README.md`).
- [x] SOCKS5 UDP ASSOCIATE (RFC 1928 cmd 3), so UDP captured via tun2socks is
  relayed: DNS (port 53) resolves through the policy's DoH filter, UDP/443 is
  dropped so QUIC can't bypass the MITM pipeline, and everything else is
  forwarded verbatim over per-destination sockets
  (`internal/proxy/socks5_udp.go`).

## Classifier quality

- [ ] Train the text Bayesian model from real labeled corpora instead of
  synthetic per-wordlist counts (`scripts/build_text_bayes_model.go`
  currently assigns fixed adult/safe pseudo-counts per seed phrase).
- [ ] Non-English adult-text coverage — only the English LDNOOBW-derived
  seed vocabulary is embedded today (see
  `internal/classify/textbayes/NOTICE` for licensing constraints on
  additional sources).
