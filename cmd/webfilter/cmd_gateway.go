package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/gateway"
)

// newGatewayCmd inspects and cleans up gateway (network routing) mode, the
// headless equivalent of the Settings page's card. Enabling it is a settings
// change; these subcommands are for seeing what it did and undoing it.
func newGatewayCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "gateway",
		Short: "Inspect and clean up gateway mode (filtering other machines' traffic)",
		Long: "Gateway mode makes this host a filtering router: other machines route\n" +
			"through it, nftables redirects their web traffic into the filter, and\n" +
			"their real source addresses are preserved so per-client policies apply.\n" +
			"Linux only. Enable it in Settings or settings.json.",
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Report gateway configuration, privileges, and prerequisites",
	}
	sf := addConfigFlags(status)
	status.RunE = func(cmd *cobra.Command, args []string) error {
		return runGatewayStatus(sf.settingsPath)
	}

	cleanup := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove the nftables ruleset gateway mode installs",
		Long: `Gateway mode removes its own ruleset when the service stops. This is the
way back if it did not - a hard kill, a power cut, or an older build.

It deletes only the "webfilter" nftables table, so any other firewall rules on
the host are left alone. Safe to run when there is nothing to remove.`,
	}
	cf := addConfigFlags(cleanup)
	cleanup.RunE = func(cmd *cobra.Command, args []string) error {
		return runGatewayCleanup(cmd.Context(), cf.settingsPath)
	}

	root.AddCommand(status, cleanup)
	return root
}

func runGatewayStatus(settingsPath string) error {
	settings, err := config.LoadSettings(settingsPath)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	st := gateway.Inspect(settings)

	fmt.Printf("platform:   %s (supported: %t)\n", st.Platform, st.Supported)
	fmt.Printf("enabled:    %t\n", st.Enabled)
	fmt.Printf("interface:  %s\n", orAny(st.Interface))
	fmt.Printf("ports:      %v\n", st.InterceptPorts)
	fmt.Printf("drop_quic:  %t\n", st.DropQUIC)
	fmt.Printf("ip_forward: %t\n", st.IPForward)
	fmt.Printf("privilege:  %s (ok: %t)\n", st.Privilege, st.PrivilegeOK)
	if st.NftPresent {
		fmt.Printf("nft:        %s\n", st.NftPath)
	} else {
		fmt.Println("nft:        not installed (apt install nftables)")
	}
	if st.LastError != "" {
		fmt.Printf("problem:    %s\n", st.LastError)
	}
	return nil
}

func orAny(s string) string {
	if s == "" {
		return "(any)"
	}
	return s
}

func runGatewayCleanup(ctx context.Context, settingsPath string) error {
	// Deleting an nftables table needs the same privileges as creating one.
	// Without them nft fails and a best-effort teardown would report success,
	// which is the worst possible answer for someone whose gateway is wedged.
	if ok, detail := gateway.HasNetAdmin(); !ok {
		return fmt.Errorf("cannot remove the gateway ruleset: %s\n"+
			"       run it as root, e.g. sudo %s gateway cleanup --settings %s",
			detail, os.Args[0], settingsPath)
	}
	fmt.Printf("[gateway] deleting the %q nftables table ...\n", gateway.TableName)
	gateway.Cleanup(ctx)
	fmt.Println("[gateway] done (a \"No such file or directory\" above just means there was nothing to remove)")
	return nil
}
