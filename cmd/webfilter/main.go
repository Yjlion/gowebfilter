// Command webfilter is a single-binary port of mitmproxy-web-filter: a MITM
// filtering proxy plus its management API/UI, sharing config via the
// filesystem (config/settings.json, policies/*.json, the SQLite log DB).
package main

import (
	"fmt"
	"os"

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
		newCategoriesCmd(),
		newOuiCmd(),
		newServiceCmd(),
		newVersionCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
