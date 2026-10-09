# Screenshots

Captures of the management web UI, rendered against **generated sample data** —
these are not real traffic. Every policy, log row and hostname here comes from
`scripts/seed_sample_data.go`.

| File | Page | Shows |
|---|---|---|
| `dashboard.png` | `index.html` | Proxy status, policy list with filter badges, recent blocks/requests |
| `dashboard-dark.png` | `index.html` | The same in the dark theme |
| `policies.png` | `policies.html` | All policies, with `Scheduled`/`Inactive` and filter badges (Ads, Censor, ...) |
| `policy-editor.png` | `policy-editor.html?name=kids` | The per-policy filter sections, collapsed |
| `editor-url-filter.png` | policy editor, URL Filter | Allow/block lists and per-category Block / Allow / Off |
| `editor-adblock.png` | policy editor, Ad & Tracker Blocking | Filter lists with install status, cosmetic filtering, exempt sites |
| `editor-text-classifier.png` | policy editor, Text Classifier | Block / censor / both, extra censor words, languages, threshold |
| `editor-safesearch.png` | policy editor, SafeSearch | Per-engine SafeSearch and tab blocking |
| `logs.png` | `logs.html` | Block log |
| `logs-requests.png` | `logs.html`, All Requests | Request log with allowed / modified / blocked actions |
| `logs-policy-changes.png` | `logs.html`, Policy Changes | Policy-edit audit trail |
| `analytics.png` | `analytics.html` | Top blocked domains, blocks by filter, hourly timeline, per-device |
| `tools.png` | `tools.html` | Classifier Health, NSFW URL Scanner, YouTube decoder, DoH query, Public IP, Policy Simulator |
| `settings.png` | `settings.html` | Listen addresses and the TUN/tun2socks section |
| `settings-adblock.png` | `settings.html`, Ad & Tracker Filter Lists | List directory, refresh interval, custom lists, per-list update/delete |
| `login.png` | `login.html` | Sign-in page |
| `block-page.png` | through the proxy | The block page a browser sees |
| `censor-before.png` | sample article, direct | A page with ads and profanity, as published |
| `censor-after.png` | the same, through the proxy | Words censored (incl. a German quote, via its `lang`), ad elements hidden |

## Regenerating

```bash
bash scripts/capture_screenshots.sh
```

The script builds the binary, seeds a throwaway data directory under `$TMPDIR`,
starts `webfilter run` against it, drives headless Chromium over each page, and
deletes the temp directory afterwards. Your own `config/`, `policies/` and
`logs/` are never touched.

With Node 22+ on `PATH` the captures go through `scripts/screenshots.mjs`,
which talks to Chromium over the DevTools protocol (no npm packages). That is
what lets it expand a policy-editor section and clip to it, switch to the dark
theme, and browse through the proxy for the block-page and before/after
shots. The article for those is served by the script itself, and the seeded
`lab-pc` policy (source `127.0.0.1`) is what filters it. Without Node it falls
back to plain `chromium --screenshot` captures of the main pages. Capture a
subset with `--only dashboard,editor-adblock`.

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
