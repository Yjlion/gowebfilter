# Deploying WebFilter Proxy

WebFilter Proxy ships as a single binary per OS/arch (Windows x86_64,
Linux x86_64/arm64) - there is no Python runtime, virtualenv, package, or
native ML shared library to install. The text classifier is an embedded
pure-Go Bayesian scorer, and the image classifier is pure Go with its model
embedded in the binary. Deployment is just: put the binary somewhere and
run it as a long-lived process. On first start, WebFilter creates
`config/settings.json` and `policies/default.json` if they are missing, then
creates `certs/`, `logs/`, and other runtime directories as needed. Release
archives include `categories/`, plus example settings/policy files for
reference.

This directory contains the pieces needed to run it as an actual system
service rather than a foreground process.

## Linux (systemd)

1. Build or download the `linux-amd64`/`linux-arm64` binary.
2. Run the installer as root:

   ```bash
   sudo ./packaging/install.sh --mode run
   ```

   This creates a `webfilter` system user, installs the binary and unit file
   into `/opt/webfilter`, seeds `config/settings.json` and
   `policies/default.json` from the shipped examples if they don't already
   exist, and `systemctl enable`s the service.

   Pass `--mode split` instead to install `webfilter-proxy.service` and
   `webfilter-mgmt.service` as two independent units (process isolation
   between the proxy engine and the management UI) rather than the combined
   `webfilter.service` (`webfilter run`, both in one process - the default
   and the recommended mode for a typical single-host deployment).

   Pass `--prefix DIR` to install somewhere other than `/opt/webfilter`, and
   `--binary PATH` if the binary isn't at `<repo-root>/webfilter`.

3. Start it:

   ```bash
   sudo systemctl start webfilter.service        # --mode run
   # or:
   sudo systemctl start webfilter-proxy.service webfilter-mgmt.service   # --mode split
   ```

4. Open `http://<host>:8000` for the management UI, and trust
   `/opt/webfilter/certs/ca.crt` in any browser/OS that should have its
   HTTPS traffic filtered (mitmproxy's usual "install the CA cert" step -
   unavoidable for any TLS-intercepting proxy).

The three unit files in this directory (`webfilter.service`,
`webfilter-proxy.service`, `webfilter-mgmt.service`) can also be installed
by hand if you'd rather not run the installer - just adjust the `User`,
`WorkingDirectory`, and `ExecStart` paths to match your layout.

### Debian/Ubuntu (.deb)

Release tags also attach `webfilter_<version>_amd64.deb` and
`webfilter_<version>_arm64.deb` (built by `scripts/build-deb.sh`, invoked
from `scripts/package-release.sh`). Install with:

```bash
sudo apt install ./webfilter_<version>_<arch>.deb
```

This is the `webfilter run`-mode equivalent of `install.sh` above (creates
the `webfilter` system user, installs to `/opt/webfilter`, seeds
`config/settings.json`/`policies/default.json` from the bundled examples,
and enables but doesn't start `webfilter.service`) driven by the package's
own postinst/postrm instead of a separate script, so ownership and
permissions come out correct from a plain `dpkg -i`/`apt install` with no
extra step. `apt remove` disables the service and leaves `/opt/webfilter`
in place; `apt purge` also removes the `webfilter` system user and deletes
`/opt/webfilter` entirely.

## Windows (native service)

The binary has built-in Windows service support - no NSSM or other wrapper
needed. From an elevated (Administrator) prompt:

```powershell
webfilter.exe service install --settings C:\path\to\config\settings.json
webfilter.exe service start
```

Other subcommands: `webfilter.exe service stop`, `service uninstall`,
`service status`. All accept `--name` to manage a service under a name other
than the default `WebFilterProxy` (useful for running more than one
instance). The installed service always launches `webfilter run` under
`Local System` with automatic startup; edit the service's logon account
afterward via `services.msc` if you'd rather run it under a dedicated
account.

As with Linux, once it's running, open `http://localhost:8000` for the
management UI and trust the generated `certs\ca.crt`.

## Desktop tray

On Windows, `webfilter.exe run` launched interactively (not by the Service
Control Manager - see above) always shows a system tray icon, since an
interactive session always has a desktop to show it on. Its one menu item
opens the management UI; left-clicking the icon does the same. Set
`"disable_tray": true` in `settings.json` to opt out and get a plain
foreground run instead. Linux/macOS never auto-show it, since `webfilter run`
there is routinely started headless (e.g. under systemd).

The tray is also available as a standalone command on any platform:

```powershell
webfilter.exe tray --settings C:\path\to\config\settings.json
```

Service/headless operation does not depend on the tray either way.

## Native desktop GUI

`webfilter gui` opens a native management window (dashboard, policies,
logs, settings). Like the tray, it self-hosts the proxy + management server
when nothing is listening on the management port, and attaches to an
existing service/`run` otherwise — closing the window only stops a
self-hosted engine. Headless deployments are unaffected: the GUI toolkit is
compiled into every binary (it grows the binary by roughly 19 MB) but a
display (X11/Wayland on Linux) is only needed when the `gui` command is
actually run.

## TUN / tun2socks capture

TUN capture is configured in the management UI under Settings ->
`TUN / tun2socks`. It is disabled by default. When enabled, WebFilter routes
this machine's traffic through a TUN device and into the filtering proxy.
Normal policy routing, MITM, logging, category filtering, SafeSearch, and
classifiers all still apply.

Capture is performed by the **official `tun2socks` binary
(<https://tun2socks.com>), run as a separate child process** — so only that
process needs Administrator/root, not the filtering engine itself. Install it
with the **Download tun2socks** button on the Settings page, or headlessly:

```bash
webfilter tun2socks download   # fetch + sha256-verify into ./bin/
webfilter tun2socks status     # binary, privileges, and device state
```

The binary is installed into a `bin/` directory beside the `webfilter`
executable, so a relocated install carries it along. Packaged installs should
either ship it there or run the download once post-install; the `.deb` and the
release archives do not bundle it (it is ~11 MB and platform-specific).

There is no proxy-target setting. WebFilter creates a dedicated SOCKS5
listener for capture, on a port the OS assigns, and points tun2socks at it.
That listener is not part of `proxy_listen` and cannot be edited — tun2socks
needs a SOCKS5 endpoint that carries UDP, so there is nothing useful to
configure. DNS is filtered through the policy's DoH resolver, other UDP is
relayed, and UDP/443 is dropped so QUIC cannot bypass filtering.

### How capture is routed (Linux)

Capture never touches the main routing table. It builds a private one and
selects it with policy rules, which is what keeps the host recoverable:

```
table 8888:  default via <tun_gateway> dev <device_name>
             throw <cidr>            for each tun2socks.bypass_cidrs entry
ip rule 9000:  fwmark 0x5745 -> main    the engine's own traffic
ip rule 9100:                -> 8888    everything else
```

Three consequences worth knowing:

- **The host's real default route is never displaced.** Recovery is therefore
  always one command away, and a reboot always fixes it, because `main` still
  holds the route it always did. That is the difference from the old
  main-table design, where the displaced default had to be reconstructed by
  hand. It is *not* a claim that a crash cannot interrupt traffic: `ip tuntap
  add` creates a **persistent** device, so if capture is killed without
  cleaning up, the device stays up with nothing reading it and the capture
  table black-holes what it selects. That is what the layered teardown below
  is for.
- **Rule 9000 is what stops the engine looping.** The filtering engine marks
  its own outbound sockets with `SO_MARK 0x5745`, so its upstream fetches use
  the host's normal routing. Without it, every fetch would be captured by the
  TUN and handed straight back to the engine that made it — an unbounded loop.
  This is the reason `CAP_NET_ADMIN` is needed even for a run that never
  touches `ip`: setting `SO_MARK` requires it too.
- **`bypass_cidrs` are `throw` routes**, which end the lookup in table 8888
  without matching, so the kernel carries on to `main`. The defaults keep
  loopback and the RFC1918 ranges off the tunnel.

Teardown is layered, because no single mechanism covers every way a process
can die:

1. **The engine removes its own routing on shutdown**, and blocks until it is
   gone rather than racing its own exit.
2. **`ExecStopPost` in the drop-in** repeats the cleanup for a crashed or
   OOM-killed engine that never reached step 1. Note it does *not* help
   against `systemctl kill -s KILL`, which SIGKILLs the whole control group
   including the hook.
3. **The next start pre-cleans** before configuring, so a run that skipped
   both of the above is reclaimed rather than stacking duplicate rules on top.

To do it by hand:

```bash
sudo -u webfilter /opt/webfilter/webfilter tun2socks cleanup --settings /opt/webfilter/config/settings.json
```

`tun2socks.dns_servers` and `tun_netmask` deserve a note: `dns_servers` is
applied on Windows only (via `netsh`). On Linux it has no effect, because
captured DNS is answered by the policy's own DoH resolver rather than by a
resolver configured on the device. `tun_netmask` is honoured on both.

Windows requires an elevated Administrator process and `wintun.dll`. The
tun2socks release archive does **not** include it: place the matching
architecture DLL beside `webfilter.exe` or in `System32`. If the DLL is
missing, WebFilter stays up and reports TUN as unavailable instead of exiting.
macOS route setup is not wired in this release.

### Linux: running TUN capture under systemd

The shipped units run as the unprivileged `webfilter` user, so out of the box
TUN capture is skipped with:

```
WARN tun2socks not started err="tun2socks disabled for this run because the
privileges it needs are unavailable: not running as root and CAP_NET_ADMIN is
not held; ..."
```

The fix is `packaging/tun2socks.conf`, a systemd drop-in granting exactly two
capabilities:

- **`CAP_NET_ADMIN`** — the `ip tuntap` / `ip addr` / `ip link` / `ip route`
  calls, and the tun2socks child's `TUNSETIFF` on `/dev/net/tun`.
- **`CAP_NET_RAW`** — `SO_BINDTODEVICE`, which tun2socks uses whenever
  `tun2socks.interface_name` is set (it passes `--interface`). Without it,
  capture still starts but that binding fails; `webfilter tun2socks status`
  says so explicitly.

`install.sh` installs the drop-in automatically when the settings file it is
installing has tun2socks enabled. Force or suppress it with `--tun2socks` /
`--no-tun2socks`. To add it by hand later:

```bash
sudo install -D -m 0644 packaging/tun2socks.conf \
  /etc/systemd/system/webfilter.service.d/10-tun2socks.conf
sudo systemctl daemon-reload && sudo systemctl restart webfilter.service
```

In `--mode split` it goes on `webfilter-proxy.service.d/` instead:
`webfilter mgmt` never supervises tun2socks, so the mgmt unit never needs it.

Things that are easy to get wrong here:

- **`setcap cap_net_admin+ep bin/tun2socks` does not work.** The units set
  `NoNewPrivileges=true`, which disables file capabilities outright — and it
  is the `webfilter` process, not the child, that runs `ip`. Ambient
  capabilities are used precisely because they survive `execve`, so both the
  `ip` calls and the tun2socks child inherit them.
- **Do not add `PrivateDevices=yes`.** It replaces `/dev` with a minimal set
  that has no `tun`. `ProtectSystem=strict` is fine as-is: it exempts `/dev`.
- **In split mode the Settings page under-reports privileges.** That card is
  rendered by `webfilter mgmt`, so it describes *that* process — it will say
  "needs admin/root or CAP_NET_ADMIN" even while `webfilter-proxy.service`
  holds the capabilities and capture is running.
- **Security tradeoff, stated plainly:** with the drop-in the filtering engine
  holds these two capabilities for its whole lifetime, so the usual "only the
  tun2socks child is privileged" claim becomes "the engine holds two
  capabilities instead of root, and the child inherits them". That is still
  much narrower than `User=root`, and it is the only option compatible with
  `NoNewPrivileges=true` (capabilities cannot be dropped process-wide from Go,
  since they are per-thread on Linux). Compare before/after with
  `systemd-analyze security webfilter.service`.
- **`SO_MARK` needs `CAP_NET_ADMIN` too**, not just the `ip` calls. That is
  fine under the drop-in, and it means the two travel together: any process
  privileged enough to install the capture routing is privileged enough to
  exempt itself from it. If the mark is ever refused, the engine logs
  `could not mark upstream socket for TUN-capture bypass` once and keeps
  serving — treat that line as "capture is looping, fix the capabilities".
  Operators who cannot grant `CAP_NET_ADMIN` can get the same exemption from
  outside with a uid rule instead, e.g.
  `ip rule add pref 9000 uidrange 102-102 lookup main` for the `webfilter`
  user — but note that exempts *everything* that user runs, not just the
  engine's upstream sockets.
- **IPv6 is not captured.** The TUN device is IPv4-only, so a host with an
  IPv6 default route reaches dual-stack sites over v6, unfiltered. WebFilter
  logs a warning at capture start when it finds one. Disable IPv6 on the host
  if that matters.

To verify the capability grant without touching routing at all:

```bash
sudo systemd-run --pty --uid=webfilter --gid=webfilter \
  --working-directory=/opt/webfilter --property=NoNewPrivileges=yes \
  --property='AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW' \
  /opt/webfilter/webfilter tun2socks status --settings config/settings.json
```

It should print `privilege:  CAP_NET_ADMIN (ok: true)`; drop the
`AmbientCapabilities` property and it reports `ok: false` with the remedy.

## Building a release archive locally

`scripts/package-release.sh` cross-compiles all three targets and produces
the tarballs/zip a GitHub release would attach - see that script for usage.
