package tun2socks

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/yjlion/gowebfilter/internal/models"
)

// EgressMark is the firewall mark the filtering engine stamps on its own
// outbound sockets (see proxy.SetUpstreamEgressMark). configureLinux installs
// a matching `ip rule ... fwmark <EgressMark> lookup main`, which is what stops
// the engine's upstream fetches from being captured by the TUN device it just
// installed a route for - without it, every fetch loops back into the engine.
//
// Declared here rather than in platform_linux.go because the callers that set
// the mark are built for every platform; only the `ip rule` half is Linux-only.
const EgressMark = 0x5745 // 'WE'

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type osCommandRunner struct{}

func (osCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		cmdline := name + " " + strings.Join(args, " ")
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("%s: %w", cmdline, err)
		}
		return fmt.Errorf("%s: %w: %s", cmdline, err, msg)
	}
	return nil
}

func configurePlatform(ctx context.Context, cfg models.Tun2SocksConfig, runner commandRunner) error {
	switch runtime.GOOS {
	case "windows":
		return configureWindows(ctx, cfg, runner)
	case "linux":
		return configureLinux(ctx, cfg, runner)
	default:
		return fmt.Errorf("tun2socks route setup is not supported on %s", runtime.GOOS)
	}
}

// unconfigurePlatform removes whatever configurePlatform installed. It is
// best-effort by design and reports nothing: it runs on the shutdown path,
// where every command is expected to fail once for the ordinary reason that
// the state it removes is already gone.
//
// This is the single teardown implementation - the supervisor's shutdown, the
// pre-clean before a fresh configure, and `webfilter tun2socks cleanup` all
// route through here, so there is no second version to drift.
func unconfigurePlatform(ctx context.Context, cfg models.Tun2SocksConfig, runner commandRunner) {
	switch runtime.GOOS {
	case "windows":
		unconfigureWindows(ctx, cfg, runner)
	case "linux":
		unconfigureLinux(ctx, cfg, runner)
	}
}

// HasRoutePrivileges reports whether this process can create or remove the TUN
// device and its routing rules, and a human-readable reason when it cannot.
// Exported for `webfilter tun2socks cleanup`, which would otherwise run every
// `ip` command, have each one fail, and still report success.
func HasRoutePrivileges() (bool, string) {
	return hasRoutePrivileges()
}

// Cleanup tears down any TUN device and routing state a previous run left
// behind. Exposed for `webfilter tun2socks cleanup`, the documented way back
// from a wedged capture, and for the unit's ExecStopPost hook.
func Cleanup(ctx context.Context, cfg models.Tun2SocksConfig) {
	unconfigurePlatform(ctx, cfg, osCommandRunner{})
}
