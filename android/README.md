# WebFilter for Android

On-device port of `gowebfilter`. The whole pure-Go filtering engine (proxy
pipeline, CA, classifiers, mgmt UI) is embedded via **gomobile** and fed by an
Android **VpnService** TUN through **tun2socks** — no external proxy, no root.

```
VpnService TUN ─► tun2socks (fd://, in Go) ─► 127.0.0.1:1080 SOCKS5
              ─► proxy.Engine → addon pipeline → CA/leaf certs
Native UI ─► gomobile API (settings/policies/logs JSON, no HTTP)
Web UI (opt-in): WebView ─► 127.0.0.1:8000 (embedded chi server, offline assets)
```

This directory is a standard Android Studio / Gradle project. The one thing it
does **not** contain is the compiled Go binding (`app/libs/webfilter.aar`) —
that is a build artifact you produce from the Go module in the repo root.

## Prerequisites

- **Android SDK** (API 34) + **NDK** (r26+) — install via Android Studio or
  `sdkmanager`. Point `ANDROID_HOME`/`ANDROID_NDK_HOME` at them (or set
  `sdk.dir`/`ndk.dir` in `local.properties`).
- **Go 1.24+** and **gomobile** — install from inside the repo checkout
  (no `@latest`) so the binaries match the `golang.org/x/mobile` version
  pinned in `go.mod` via its `tool` directive; recent gomobile releases
  refuse to bind when x/mobile is missing from the module graph:
  ```bash
  go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
  gomobile init
  ```

## Build

From the **repository root**: if you're including the emulator ABI
(`android/amd64`), first remap `modernc.org/libc`'s legacy syscalls — on
x86_64, Android's app seccomp policy SIGSYS-kills the process at the first
sqlite open without it (arm64/arm don't need this, but the patch is harmless
there):

```bash
go run scripts/patch_libc_seccomp.go   # adds a go.mod replace; -undo reverts
```

Then build the AAR from the `mobile/` Go package:

```bash
gomobile bind -target=android/arm64,android/arm,android/amd64 -androidapi 26 \
    -o android/app/libs/webfilter.aar ./mobile
```

(Drop `android/amd64` and skip the patch if you only need real devices —
remember not to commit the `replace` line the patch adds to `go.mod`.)

Then build the app:

```bash
cd android
./gradlew assembleDebug
# APK: app/build/outputs/apk/debug/app-debug.apk
```

Install and run on a device/emulator:

```bash
./gradlew installDebug
```

> The Gradle wrapper is pinned to Gradle 8.7. The bundled
> `gradle-wrapper.properties` sets `validateDistributionUrl=false` so the
> wrapper works behind restrictive proxies; the distribution is still fetched
> from `services.gradle.org` on first run — override `distributionUrl` if you
> mirror it internally.

### Build via GitHub Actions (no local SDK needed)

The repository has a manual workflow, **Actions → Android APK → Run
workflow** (`.github/workflows/android.yml`), that performs the exact steps
above on a GitHub runner and uploads two artifacts: `webfilter-debug-apk`
(the installable debug APK) and `webfilter-aar` (the gomobile binding, if
you only want to rebuild the app locally). The same workflow also runs as
part of `ci.yml`'s release job on `v*` tags, so every GitHub release
carries a `webfilter-<version>-android-debug.apk` asset. The APK is
debug-signed; release signing is not wired up yet.

## Using the app

The app is a single screen with four tabs (Home · Filters · Activity ·
Settings).

0. **First run** — a short welcome guide. It states plainly that **the app
   does not enforce anything on its own**: the VPN can be switched off, the
   CA removed, settings changed and the app uninstalled by anyone holding
   the phone. Enforcement needs an EMM/MDM (always-on VPN with lockdown,
   `settings_locked`, a pushed CA, uninstall blocking — see below). On a
   managed device the guide says so instead. The guide then pre-selects the
   installed **web browsers** as the filtered apps, offers the CA setup, and
   ends with "Start protection". It can be re-opened from Settings › About.
1. **Home** — the integrated dashboard: a large shield toggle (VPN consent,
   TUN, engine start; or a proxy-only start, see below), 24-hour totals
   (requests, blocked, block rate), an hourly blocks chart, a status list
   (CA installed or not, filtered apps, the policy currently applied to the
   device and which filters it has on, enforced/not enforced), recently
   blocked domains and the top blocked domains. Everything comes from the
   gomobile API (`analyticsJson`, `queryLogsJson`, `activePolicyName`), so
   it works with the web interface off.
2. **Filters** — one expandable card per policy. The **Default policy** is
   the always-on fallback; expanding a card lists its filter modules
   (SafeSearch, ad & tracker blocking, DNS over HTTPS, URL filter,
   categories, YouTube, text/image classifiers, block page, and schedule
   for non-default policies) with an inline on/off switch and a tap-through
   to the module's detail screen. "Add policy" creates a named override
   policy (e.g. "bedtime", created Inactive); give it a schedule and switch
   it Active: while inside a window it overrides default (all device traffic
   shares one client IP, so schedules — not source IPs — vary rules
   on-device). An Active policy without a schedule competes with default
   alphabetically, which the card warns about. Rename/delete is in each
   card's menu; "default" cannot be renamed, deactivated or deleted.
   - A **fresh install** starts with a preconfigured "family baseline"
     default policy (written once by `mobile/settings.go` on first
     provisioning): SafeSearch on every engine, ad & tracker blocking
     (EasyList + EasyPrivacy), DNS through Cloudflare for Families
     (`1.1.1.3`) and QUIC discouraged. Classifiers and URL categories stay
     off. Existing installs and MDM `policy_json` pushes are not touched.
   - **Categories** downloads [ipfire lists](https://dbl.ipfire.org/lists/)
     onto the device (gzip-compressed, shared by all policies) and sets
     each to block/allow/off for that policy.
3. **Activity** — the request/block/policy-change logs with filter chips
   and search; works with filtering stopped. **Analytics** (totals, top
   domains, blocks by component, hourly timeline) is linked from Home and
   Settings.
4. **Settings**
   - **Filtered apps** — browsers are listed first (with "Select all");
     "Filter all apps" routes every app. Android applies the allowed-app set
     when the tunnel is established, so stop/start protection to apply.
   - **CA certificate** — a guided flow with a live "installed?" check
     (the CA's alias in `AndroidCAStore`, re-checked whenever you return to
     the screen). Step 1 saves `webfilter-ca.crt` straight into Downloads
     (MediaStore, no permission; a save dialog on Android 8–9). Step 2: on
     Android 11+ the KeyChain installer refuses CA certificates, so the app
     opens Security settings and spells out the path (Encryption &
     credentials › Install a certificate › CA certificate, with Samsung and
     Xiaomi variants); on Android 8–10 it uses the KeyChain installer
     directly. The limits card explains what needs the CA: URL / hostname
     (SNI) / DoH / QUIC filtering work for every routed app *without* it;
     deep content features (SafeSearch rewriting, cosmetic ad hiding,
     classifiers, YouTube) need it and only apply to apps that trust user
     CAs. Chrome and many hardened apps reject the user CA — their traffic is
     blind-spliced, not broken.
   - **Proxy-only mode (no VPN)** starts the engine *without* a TUN:
     nothing is captured system-wide, no VPN consent, and only clients
     configured against the loopback HTTP proxy (`127.0.0.1:8080`) or its
     PAC file (`http://127.0.0.1:8000/proxy.pac`, shown on Home with
     tap-to-copy) are filtered. Intended for a managed device where the EMM
     pushes Chrome's `ProxySettings` policy at that PAC URL — whether a
     given Chrome build honors a loopback PAC pushed that way needs
     verification against your EMM. The `proxy_only_mode` restriction
     forces it.
   - **Advanced › Web interface** — the full management web UI is **off by
     default**. While off, the Go engine's loopback server answers only
     `/proxy.pac`, `/wpad.dat`, `/wpad.da`, `/api/ca-cert` and `/health`
     and 404s everything else (`mgmtapi.Server.WebUIDisabled`, driven by
     `Mobile.setWebUiEnabled`); turning it on serves the UI again at once
     and shows an "Open web interface" entry (in-app WebView). General
     (logging, retention, web UI language) and Security (web UI password)
     settings live here too. Engine-side settings edits need a filter
     restart; policy edits hot-reload.

## Managed configuration (MDM/EMM)

The app declares a managed-configuration schema
(`app/src/main/res/xml/app_restrictions.xml`) so an EMM portal (Intune,
Workspace ONE, MobileIron, TestDPC, ...) can provision it:

- **Typed keys** for the common settings (SafeSearch, URL filter lists,
  YouTube, classifiers, DoH, logging, dashboard password). Only keys the
  admin actually sets are applied — a partial push never clobbers unmanaged
  on-device edits. Lists are newline-separated strings; thresholds are
  strings ("0.8") because Android restrictions have no float type.
- **Newer typed keys**: `url_filter_category_actions` (one `name:action`
  per line, action `block` or `allow`; it replaces the policy's category
  actions and wins over the legacy `url_filter_categories` block list),
  `text_classifier_mode` (`block` / `censor` / `both`),
  `text_classifier_censor_words`, and `adblock_enabled`, `adblock_lists`
  (preset names, one per line), `adblock_cosmetic`, `adblock_allow`.
- **`web_ui_enabled`** — forces the Advanced › Web interface switch on or
  off. Like `proxy_only_mode` it is consumed by the Kotlin layer only (it
  calls `Mobile.setWebUiEnabled`, it is not engine config).
- **`policy_json`** — a full policy document that replaces the device's
  `default` policy wholesale, for admins who outgrow the typed keys
  (`schedule_json` is the same escape hatch scoped to the schedule).
- **`settings_locked`** — the lockdown switch. When set, the **Go engine
  itself** rejects every config mutation (native UI, WebView dashboard, and
  any on-device `curl` to 127.0.0.1:8000 all get HTTP 403); the native
  settings screens render read-only with a "managed by your organization"
  banner. Reads, status, and logs stay available. Pair it with the EMM's
  **always-on VPN + lockdown** device policy so the user cannot simply stop
  the filter.

Restrictions are applied on app start, before every engine start, and on the
EMM's push broadcast while the process is alive (a push that arrives while
the process is dead lands at the next start). Re-applying an unchanged
configuration is a no-op — the applied document is hashed, so the dashboard
password is not re-hashed (and sessions not invalidated) on every boot.
Un-enrolling (clearing all restrictions) unlocks the device but keeps the
last-applied values.

### Verifying with TestDPC

1. Install [TestDPC](https://github.com/googlesamples/android-testdpc) on a
   device/emulator and set it as device or profile owner.
2. In TestDPC: *Manage app restrictions* → select WebFilter → set e.g.
   `safesearch_enabled=true` and `settings_locked=true` → Save.
3. Launch WebFilter → the Filters tab shows the managed banner, switches
   are disabled and detail screens are read-only; Home says "Managed by
   your organization". With the web interface on (or `web_ui_enabled=true`)
   and the VPN running, `GET http://127.0.0.1:8000/api/policies/default`
   shows `"safesearch":{"enabled":true,...}`.
4. Try saving anything in the dashboard's settings page → the API returns
   403 `{"detail":"Settings are managed by your organization..."}`.
5. Clear the restrictions in TestDPC → the banner disappears and edits work
   again.

## Verifying filtering works

The logs are the source of truth, not HTTP status codes (blocked responses
return **HTTP 200** with a block page). The Activity tab shows them; or, with
Settings › Advanced › Web interface on and the VPN running, from a shell on
the device:

```bash
# blocked and per-request decisions (action: ok/modified/blocked, component)
curl -s "http://127.0.0.1:8000/api/logs?kind=requests&limit=20"
curl -s "http://127.0.0.1:8000/api/logs?kind=blocks&limit=20"
```

## What is verified vs. not

- **Verified in CI:** the Go `mobile/` package cross-compiles for
  `android/arm64` (`GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build ./mobile
  ./internal/...`, in `ci.yml`'s matrix) and its pure-Go logic passes
  `go test ./mobile` on the host. The `fd://` device scheme is present in the
  pinned `xjasonlyu/tun2socks v2.6.0`. The manual `android.yml` workflow
  builds the full AAR + APK on demand.
- **Verified on an emulator** (x86_64 API 34 image, AAR built with the libc
  seccomp patch): first-run guide, browser pre-selection, the preconfigured
  default policy, VPN start from the Home shield with only the selected
  apps' UIDs on the VPN, Home stats/recent blocks from the real log store,
  inline policy-module toggles persisting to `policies/default.json`, the
  web UI gate (404 off / 200 on, PAC and CA cert always 200), saving the CA
  to Downloads, and the "installed" check flipping once the CA is in the
  user trust store.
- **Not yet verified on real hardware:** `modernc.org/sqlite` behavior under
  a real device's Android runtime, on-device image-CNN latency/battery, the
  Settings-app CA install path on specific OEM builds, and long-running
  VpnService→tun2socks→engine stability.

## Layout

```
android/
├── NOTICE                          Material icon licence
├── app/
│   ├── build.gradle.kts            consumes app/libs/*.aar
│   ├── libs/                       drop webfilter.aar here (gitignored)
│   └── src/main/
│       ├── AndroidManifest.xml     VpnService + permissions + FileProvider
│       ├── java/com/webfilter/app/
│       │   ├── App.kt                   applies MDM restrictions + web UI switch, push receiver
│       │   ├── MainActivity.kt          tab shell (bottom nav), start/stop, VPN consent
│       │   ├── HomeFragment.kt          integrated dashboard
│       │   ├── FiltersFragment.kt       expandable policy cards + inline module switches
│       │   ├── PolicyModules.kt         the module list both of those render
│       │   ├── LogsFragment.kt          Activity tab: request/block/audit logs
│       │   ├── AppSettingsFragment.kt   Settings tab (apps, CA, mode, web UI, about)
│       │   ├── OnboardingActivity.kt    first-run guide (enforcement notice, browsers, CA)
│       │   ├── EnforcementInfo.kt       the "no enforcement without an EMM" dialog
│       │   ├── WebUiActivity.kt         opt-in management web UI in a WebView
│       │   ├── WebFilterVpnService.kt   TUN + Mobile.start / proxy-only start
│       │   ├── AppPickerActivity.kt     per-app selection (browsers first, search)
│       │   ├── BrowserApps.kt           installed-browser detection
│       │   ├── CaInstallActivity.kt     guided CA install with live status
│       │   ├── CaStatus.kt              CA PEM, AndroidCAStore check, save to Downloads
│       │   ├── SettingsActivity.kt      hosts one native preference screen (PrefsFragment)
│       │   ├── ScheduleActivity.kt      time-window editor for a policy's schedule
│       │   ├── CategoriesActivity.kt    ipfire category downloads + actions
│       │   ├── AnalyticsActivity.kt     totals, top domains, hourly timeline
│       │   ├── BarsView.kt / Rows.kt    shared chart + icon-row helpers
│       │   ├── BaseActivity.kt          up navigation for secondary screens
│       │   ├── ConfigStores.kt          JSON stores over the gomobile API
│       │   ├── PreferenceDataStores.kt  preference-key → JSON-path mapping
│       │   ├── ManagedConfig.kt         RestrictionsManager bundle → managed doc
│       │   └── Prefs.kt                 app-side prefs (apps, mode, web UI, first run)
│       └── res/                    layouts, strings, prefs XML, icons, app_restrictions
├── build.gradle.kts / settings.gradle.kts / gradle.properties
└── gradle/ + gradlew               Gradle 8.7 wrapper
```

The gomobile package name is `mobile`, so the generated Java class is
`mobile.Mobile` with static methods `start(String, long)`,
`startProxyOnly(String)`, `stop()`, `isRunning()`, `mgmtUrl()`, `status()`,
`activePolicyName()`, `setWebUiEnabled(boolean)`, `webUiEnabled()`,
`reloadPolicies()`, `caCertPem(String)`, and — for the native UI and MDM
path, all JSON-string in/out — `getSettingsJson`, `updateSettingsJson`,
`getPolicyJson`, `updatePolicyJson`, `getManagedStateJson`,
`applyManagedConfigJson`, `listPoliciesJson`, `createPolicyJson`,
`deletePolicy`, `listCategoriesJson`, `downloadCategoryJson`,
`deleteCategory`, `listAdblockListsJson`, `downloadAdblockListJson`,
`deleteAdblockList`, `queryLogsJson`, and `analyticsJson` (see
`mobile/settingsapi.go`, `managed.go`, `policiesapi.go`,
`categoriesapi.go`, `adblockapi.go`, and `logsapi.go`). The native Ad &
tracker blocking screen picks lists; downloads happen automatically in the
engine, so it does not call the adblock functions yet.
