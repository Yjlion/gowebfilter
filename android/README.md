# WebFilter for Android

On-device port of `gowebfilter`. The whole pure-Go filtering engine (proxy
pipeline, CA, classifiers, mgmt UI) is embedded via **gomobile** and fed by an
Android **VpnService** TUN through **tun2socks** — no external proxy, no root.

```
VpnService TUN ─► tun2socks (fd://, in Go) ─► 127.0.0.1:1080 SOCKS5
              ─► proxy.Engine → addon pipeline → CA/leaf certs
Mgmt UI:  WebView ─► 127.0.0.1:8000 (embedded chi server, offline assets)
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

1. **Start filtering** — grants VPN consent, establishes the TUN, and starts
   the Go engine. A persistent notification shows it is active.
2. **Choose filtered apps** — pick which installed apps are routed through the
   filter. Leave everything unchecked to filter every app. (Android applies the
   allowed-app set when the tunnel is established, so toggle the VPN off/on for
   changes to take effect.)
3. **Install CA certificate** — exports the engine's CA and walks you through
   installing it. **Read the on-screen limits:** URL / hostname (SNI) / DoH /
   QUIC filtering work for every routed app *without* the certificate; deep
   content features (text & image NSFW classifiers, YouTube rewriting) require
   the CA and only apply to apps that trust user-installed CAs. Chrome and many
   hardened apps enforce Certificate Transparency and reject the user CA — their
   traffic is passed through untouched (blind-spliced), not broken.
4. **Filter settings** — native Android settings screens for the device's
   filter policy (SafeSearch per engine, URL filter, YouTube, text/image
   classifiers, DoH, schedule, block page) and the mobile-relevant globals
   (logging, retention, dashboard password). They read/write the same
   `settings.json` / `policies/default.json` as the web dashboard through the
   gomobile API, so they work even while filtering is stopped; policy edits
   hot-reload, settings edits need a filter restart (same rule as the
   dashboard). Desktop-only concerns (source IP/MAC matching, ARP scanner,
   PAC, upstream proxy) are deliberately absent — use the dashboard if you
   need them.
5. **Dashboard** — the embedded management UI loads in the in-app WebView once
   filtering is running (policies, logs, safesearch, categories, etc.).

## Managed configuration (MDM/EMM)

The app declares a managed-configuration schema
(`app/src/main/res/xml/app_restrictions.xml`) so an EMM portal (Intune,
Workspace ONE, MobileIron, TestDPC, ...) can provision it:

- **Typed keys** for the common settings (SafeSearch, URL filter lists,
  YouTube, classifiers, DoH, logging, dashboard password). Only keys the
  admin actually sets are applied — a partial push never clobbers unmanaged
  on-device edits. Lists are newline-separated strings; thresholds are
  strings ("0.8") because Android restrictions have no float type.
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
3. Launch WebFilter → the settings button shows the managed banner and
   read-only screens; `GET http://127.0.0.1:8000/api/policies/default` (with
   the VPN running) shows `"safesearch":{"enabled":true,...}`.
4. Try saving anything in the dashboard's settings page → the API returns
   403 `{"detail":"Settings are managed by your organization..."}`.
5. Clear the restrictions in TestDPC → the banner disappears and edits work
   again.

## Verifying filtering works

The mgmt API is the source of truth, not HTTP status codes (blocked responses
return **HTTP 200** with a block page). With the VPN running, browse to the
dashboard and check the logs, or from a shell on the device:

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
- **Not yet verified on real hardware:** `modernc.org/sqlite` behavior under the
  Android runtime, on-device image-CNN latency/battery, and the full
  VpnService→tun2socks→engine data path. Smoke-test on a device/emulator before
  relying on it. The Kotlin sources here have been written and reviewed but not
  compiled in CI (no Android SDK in the build environment).

## Layout

```
android/
├── app/
│   ├── build.gradle.kts            consumes app/libs/*.aar
│   ├── libs/                       drop webfilter.aar here (gitignored)
│   └── src/main/
│       ├── AndroidManifest.xml     VpnService + permissions + FileProvider
│       ├── java/com/webfilter/app/
│       │   ├── App.kt                   applies MDM restrictions, push receiver
│       │   ├── MainActivity.kt          start/stop, dashboard WebView
│       │   ├── WebFilterVpnService.kt   TUN + Mobile.start(filesDir, fd)
│       │   ├── AppPickerActivity.kt     per-app filtering selection (with icons)
│       │   ├── SettingsActivity.kt      native settings screens (PrefsFragment)
│       │   ├── ScheduleActivity.kt      time-window editor for the schedule
│       │   ├── ConfigStores.kt          JSON stores over the gomobile API
│       │   ├── PreferenceDataStores.kt  preference-key → JSON-path mapping
│       │   ├── ManagedConfig.kt         RestrictionsManager bundle → managed doc
│       │   ├── CaInstallActivity.kt     CA export + install guidance
│       │   └── Prefs.kt
│       └── res/                    layouts, strings, prefs XML, app_restrictions
├── build.gradle.kts / settings.gradle.kts / gradle.properties
└── gradle/ + gradlew               Gradle 8.7 wrapper
```

The gomobile package name is `mobile`, so the generated Java class is
`mobile.Mobile` with static methods `start(String, long)`, `stop()`,
`isRunning()`, `mgmtUrl()`, `status()`, `reloadPolicies()`,
`caCertPem(String)`, and — for the native settings UI and MDM path —
`getSettingsJson`, `updateSettingsJson`, `getPolicyJson`, `updatePolicyJson`,
`getManagedStateJson`, and `applyManagedConfigJson` (all JSON-string
in/out; see `mobile/settingsapi.go` and `mobile/managed.go`).
