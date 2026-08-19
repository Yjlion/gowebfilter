// Command webfilter is a single-binary port of mitmproxy-web-filter: a MITM
// filtering proxy plus its management API/UI, sharing config via the
// filesystem (config/settings.json, policies/*.json, the SQLite log DB).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "webfilter",
		Short: "WebFilter Proxy: MITM web filter + management UI",
		Long: "WebFilter Proxy is a policy-based, TLS-intercepting web filtering proxy\n" +
			"with a browser-based management UI. Config lives under config/settings.json\n" +
			"and policies/*.json; both the proxy and management server read/write those\n" +
			"files directly, so they can run together (`run`) or as separate processes\n" +
			"(`proxy` / `mgmt`).",
		SilenceUsage: true,
	}

	root.AddCommand(
		newRunCmd(),
		newProxyCmd(),
		newMgmtCmd(),
		newTrayCmd(),
		newGuiCmd(),
		newCategoriesCmd(),
		newTun2SocksCmd(),
		newOuiCmd(),
		newServiceCmd(),
		newVersionCmd(),
	)

	// Every long-running command takes cmd.Context(), and until this was wired
	// up that context was context.Background(): SIGTERM killed the process
	// outright, so nothing deferred ever ran. `systemctl stop` therefore left
	// the TUN device and its routing rules installed (only the unit's
	// ExecStopPost hook cleaned up, and only for people running under the
	// shipped unit), and the SQLite log store was never closed cleanly either.
	//
	// SIGTERM is what systemd and container runtimes send; os.Interrupt covers
	// Ctrl-C in a terminal. The Windows service path is unaffected - the SCM
	// stop control is handled separately in runAsWindowsServiceIfApplicable.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
