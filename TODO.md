# TODO

## Recommended Features

- [x] Policy test/simulator API (`POST /api/tools/policy-simulate`: policy
  selection, schedule status, URL allow/block/category results, addon hints).
  - [ ] Later: add a first-class UI view and deeper SafeSearch/YouTube
    simulation.
- [x] Classifier health checks (`GET /api/tools/classifier-health`: text ML
  readiness vs keyword-only fallback, embedded image classifier availability).
  - [ ] Later: surface this in the dashboard/status UI.
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
- [ ] SOCKS5 UDP ASSOCIATE. The listener is CONNECT-only (RFC 1928 cmd 1),
  so UDP-based protocols captured via tun2socks can't be relayed.

## Classifier quality

- [ ] Train the text Bayesian model from real labeled corpora instead of
  synthetic per-wordlist counts (`scripts/build_text_bayes_model.go`
  currently assigns fixed adult/safe pseudo-counts per seed phrase).
- [ ] Non-English adult-text coverage — only the English LDNOOBW-derived
  seed vocabulary is embedded today (see
  `internal/classify/textbayes/NOTICE` for licensing constraints on
  additional sources).
