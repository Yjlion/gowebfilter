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

## Optional desktop tray

Interactive desktop sessions can run:

```powershell
webfilter.exe tray --settings C:\path\to\config\settings.json
```

The tray opens the management UI, config folder, and certificate folder, and
on Windows can delegate start/stop/status actions to the built-in service
commands. The tray is optional; service/headless operation does not depend
on it.

## TUN / tun2socks capture

TUN capture is configured in the management UI under Settings ->
`TUN / tun2socks`. It is disabled by default. When enabled, WebFilter starts
a TUN device and routes captured traffic through the filtering proxy.
Leave `proxy_target` blank to use WebFilter's local SOCKS5 listener
(`socks5@127.0.0.1:1080` by default), or set an explicit
`scheme://host:port` target if you understand the routing implications.
Normal policy
routing, MITM, logging, category filtering, SafeSearch, and classifiers
still apply.

Windows requires an elevated Administrator process and `wintun.dll`.
Place the matching architecture DLL beside `webfilter.exe` or in `System32`.
If the DLL is missing, WebFilter stays up and reports TUN as unavailable
instead of exiting.
Linux requires root or equivalent capabilities for TUN and route changes.
macOS route setup is not wired in this release.

## Building a release archive locally

`scripts/package-release.sh` cross-compiles all three targets and produces
the tarballs/zip a GitHub release would attach - see that script for usage.
