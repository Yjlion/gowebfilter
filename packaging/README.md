# Deploying WebFilter Proxy

WebFilter Proxy ships as a single binary per OS/arch (Windows x86_64,
Linux x86_64/arm64) plus one bundled shared library - there is no Python
runtime, virtualenv, or package to install. The text classifier
(`internal/classify/text`) is onnxruntime-backed, and onnxruntime_go loads
its shared library dynamically rather than statically linking it, so each
release archive includes `onnxruntime.dll` (Windows) or `libonnxruntime.so`
(Linux) alongside the `webfilter` binary - keep them in the same directory
(the binary looks next to itself automatically; set
`ONNXRUNTIME_SHARED_LIBRARY` to override). The image classifier
(`internal/classify/image`) is pure Go with its model embedded in the
binary - nothing to bundle or provision for it. Deployment is just: put the
binary and the onnxruntime shared library somewhere together, give it a
working directory containing `config/settings.json` (and `policies/`,
`certs/`, `categories/`, `logs/`, `data/`, `models/` - all created on first
run if missing, except `models/` which needs
`scripts/export_text_model.py` if you want the text classifier's ML stage -
see the repo root README), and run it as a long-lived process.

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
needed. Keep `onnxruntime.dll` in the same directory as `webfilter.exe`
(the release archive already extracts them together; if you relocate the
exe, bring the DLL with it). From an elevated (Administrator) prompt:

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

## Building a release archive locally

`scripts/package-release.sh` cross-compiles all three targets and produces
the tarballs/zip a GitHub release would attach - see that script for usage.
