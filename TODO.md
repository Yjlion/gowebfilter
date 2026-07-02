# TODO

## Recommended Features

- [ ] Policy test/simulator UI and API.
  - Start with policy selection, schedule status, URL allow/block/category results, and downstream addon hints.
  - Later: add a first-class UI view and deeper SafeSearch/YouTube simulation.
- [ ] Classifier health checks.
  - Report text classifier ML readiness separately from keyword-only fallback.
  - Report embedded image classifier availability.
  - Later: surface this in the dashboard/status UI.
- [ ] Scheduled policy modes.
  - Harden schedule evaluation, including overnight windows.
  - Later: add UI presets for school hours, bedtime, weekends, and temporary unlocks.
- [ ] Policy change audit log.
- [ ] Per-client live activity dashboard.
- [ ] "Why was this blocked?" endpoint and richer block-page explanation.
- [ ] Policy import/export and validation command.
- [ ] Temporary allow/deny from the block page.
- [ ] DNS/category cache visibility in the management UI.
