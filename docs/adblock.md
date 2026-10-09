# Ad & tracker blocking

The `adblock` addon blocks ad and tracker requests using the same filter
lists browser ad blockers use, and hides leftover ad elements on pages.
It runs in the proxy for every client a policy covers, so it also covers
devices where you can't install a browser extension (smart TVs, game
consoles, kids' tablets).

## Turning it on

Per policy (web UI: *Ad & Tracker Blocking*; Android: *Ad & tracker
blocking*):

```json
"adblock": {
  "enabled": true,
  "lists": ["easylist", "easyprivacy"],
  "cosmetic": true,
  "allow": ["supported-site.example"]
}
```

| Field | Meaning |
|---|---|
| `enabled` | Off by default. |
| `lists` | Names of the lists to apply. Empty means `easylist` + `easyprivacy`. |
| `cosmetic` | Inject element-hiding CSS from the lists' `##` rules (default on). |
| `allow` | Sites where nothing is blocked. Matched against the **page** a request comes from (its `Referer`/`Origin`), so listing a site allows the ads *on* that site, wherever they are served from. Same pattern syntax as the URL filter. |

## Lists

Lists are shared by all policies and **downloaded at runtime**, never
embedded in the binary: their licences (GPLv3 / CC-BY-SA) and their weekly
churn both argue against it.

Built-in presets:

| Name | List |
|---|---|
| `easylist` | EasyList (ads) |
| `easyprivacy` | EasyPrivacy (trackers) |
| `fanboy-annoyance` | Fanboy's Annoyance (cookie banners, pop-ups) |
| `peter-lowe` | Peter Lowe's ad and tracking server list |
| `adguard-base` | AdGuard Base |
| `ublock-filters` | uBlock Origin filters |
| `ublock-privacy` | uBlock Origin privacy |

Add your own in settings (web UI: *Settings → Ad & Tracker Filter Lists*):

```json
"adblock": {
  "dir": "./adblock",
  "update_hours": 24,
  "custom_lists": [{ "name": "home-lab", "url": "https://example.com/filters.txt" }]
}
```

Names are lowercase letters, digits, `-` and `_`, and can't reuse a preset
name.

**When lists are fetched.** A list is downloaded the first time an enabled
policy uses it. Lists in use are refreshed every `update_hours` (default
24). Until a list has been downloaded and compiled, requests pass
unfiltered: adblock fails open, it never blocks everything. A failed
download keeps the previous copy and records the error, which the UI shows
as "last update failed". A response that looks like an HTML page, or a list
with no usable rules, is rejected rather than installed.

Manual control:

```bash
webfilter adblock status
webfilter adblock update                 # every installed list
webfilter adblock update easylist mine   # specific lists
```

```
GET    /api/adblock/lists                 catalog + install status
POST   /api/adblock/lists/{name}/update   download / refresh one list
POST   /api/adblock/update                refresh every installed list
DELETE /api/adblock/lists/{name}          remove a downloaded list
```

The mutating routes are refused while an Android managed configuration
locks the device. Files live in `adblock.dir` as `<name>.txt.gz` plus
`index.json`. A running proxy picks up a changed file within about 30
seconds, with no restart, even when the management server runs as a
separate process.

Downloads go through the engine's own egress dialer, so on a host running
TUN or gateway capture they are never looped back into the proxy.

## What is supported

**Formats**, detected per line: Adblock Plus / uBlock Origin syntax, hosts
files (`0.0.0.0 ads.example`) and plain domain lists (one domain per line,
in a file with no Adblock markers).

**Network rules:** `||host^` hostname anchors, `|` start/end anchors, `*`
wildcards, the `^` separator, `/regex/` (RE2 syntax), `@@` exceptions, and
these options: `$third-party`/`$3p`, `$first-party`/`$1p`, `$domain=`
(including `~` exclusions), resource types (`script`, `image`, `stylesheet`,
`xmlhttprequest`, `subdocument`, `document`, `font`, `media`, `object`,
`ping`, `websocket`, `other`, and their `~` negations), `$important`,
`$match-case`, `$all`, and the page-level exceptions `$document`,
`$elemhide` and `$generichide`.

**Cosmetic rules:** generic `##selector`, site-specific
`a.example,b.example##selector`, `~site` exclusions, and `#@#` exceptions.

**Deliberately skipped**, never approximated. Treating a rule we don't fully
implement as a plain block would block things the list author never meant
to block. A skipped rule can only let an ad through:

- rules with any other option (`$redirect`, `$csp`, `$removeparam`,
  `$popup`, `$badfilter`, `$header`, `$denyallow`, ...);
- regex rules RE2 can't compile (lookarounds, backreferences);
- scriptlets (`##+js(...)`), procedural cosmetics (`#?#`, `:has-text()`,
  `:-abp-...`, `:upward()`, ...), CSS injection (`#$#`) and HTML filtering
  (`##^`);
- entity domains (`example.*`) and regex domains in `$domain=`/cosmetic
  rules.

With EasyList + EasyPrivacy about 3% of lines are skipped. Compiling both
takes about 0.3 s and ~27 MB, and a request is matched in about 20 µs
(measured on a Xeon Gold 6252).

## How a block looks

- A **page** (a navigation the lists block) gets the normal block page,
  logged with component `adblock`.
- A **subresource** gets an empty response of the right kind: a 1×1
  transparent GIF for images, an empty script or stylesheet, and `204` for
  everything else. A block page inside a `<script>` or `<img>` slot is
  useless, and for images it renders as a broken-image icon.

Resource types come from `Sec-Fetch-Dest`, then the `Accept` header, then
the URL's extension. Third-party-ness compares registrable domains (public
suffix list) of the request and its `Referer`/`Origin`.

Every block writes a `?kind=blocks` row and a `blocked`/`adblock` row in
`?kind=requests`. On ad-heavy pages that is a lot of rows; the Analytics
page's "Blocks by Filter" panel shows how many.

## Cosmetic filtering

For each HTML page the addon injects one `<style id="webfilter-adblock">`
before `</head>` containing:

- every site-specific selector for the page's host, and
- the generic selectors whose leading class or id appears in the page.
  Sending EasyList's full generic set to every page would add hundreds of
  kilobytes; selectors keyed to a class/id the HTML never mentions can't
  match server-rendered content anyway.

Selectors are emitted one rule each (`sel{display:none!important}`), because
a browser drops a whole rule when one selector in its list is invalid.
Selectors containing `<`, `{`, `}` or `\` are refused, so a list can't break
out of the `<style>` element or inject arbitrary CSS.

Limitations: ads inserted later by JavaScript, with class names that were
not in the original HTML, aren't hidden by generic rules. Pages whose
Content-Security-Policy forbids inline styles ignore the injected CSS.

## Scope

Adblock acts on **intercepted (MITM) traffic only**. Hosts excluded from
interception (`mitm.mode: exclude`, or include-mode for unlisted hosts) are
blind-spliced. Their requests are never seen, so their ads aren't filtered,
and the connection-level host gate deliberately does not consult adblock
lists. For hostname-only blocking of such hosts, use a URL-filter block
entry or a category.

In the pipeline, adblock runs right after the URL filter
(`... UrlFilter → Adblock → QuicBlocker → ...`), so a request the URL filter
explicitly allows (a custom allow entry or an allow-category matching the
request's host) is not ad-filtered either.
