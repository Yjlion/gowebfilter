# Screenshots

Captures of the management web UI, rendered against **generated sample data** —
these are not real traffic. Every policy, log row and hostname here comes from
`scripts/seed_sample_data.go`.

| File | Page | Shows |
|---|---|---|
| `dashboard.png` | `index.html` | Proxy status, policy list, recent blocks/requests |
| `policies.png` | `policies.html` | All policies, with the `Scheduled` and `Inactive` badges |
| `policy-editor.png` | `policy-editor.html?name=kids` | The per-policy filter sections |
| `logs.png` | `logs.html` | Block log, with the Requests and Policy Changes tabs |
| `analytics.png` | `analytics.html` | Top blocked domains, blocks by filter, hourly timeline, per-device |
| `tools.png` | `tools.html` | Classifier Health, NSFW URL Scanner, YouTube decoder, DoH query, Public IP, Policy Simulator |
| `settings.png` | `settings.html` | Listen addresses and the TUN/tun2socks section |

## Regenerating

```bash
bash scripts/capture_screenshots.sh
```

The script builds the binary, seeds a throwaway data directory under `$TMPDIR`,
starts `webfilter run` against it, drives headless Chromium over each page, and
deletes the temp directory afterwards. Your own `config/`, `policies/` and
`logs/` are never touched.

It needs a Chromium/Chrome binary on `PATH`; point at a specific one with
`CHROME=/path/to/chromium`. A real browser engine is required because the UI is
an Alpine.js app that fetches its data from the management API — a static HTML
dump would come out empty.

The seeder uses a fixed RNG seed, so re-running it produces the same data and
successive screenshots stay comparable. Timestamps are relative to the run, so
those do shift.

## A note on styling

`ui/tailwind.css` is a **pre-built** stylesheet and the repo has no Tailwind
build step. If a screenshot shows an unstyled or mis-laid-out element, check
that every utility class in the markup actually exists in that file — a class
that was never compiled in (e.g. a `md:` responsive variant) silently does
nothing.
