package main

import (
	"context"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/yjlion/gowebfilter/internal/config"
	tun "github.com/yjlion/gowebfilter/internal/tun2socks"
)

// newTun2SocksCmd manages the external tun2socks binary that whole-OS TUN
// capture runs as a child process. It is the headless equivalent of the
// Settings page's tun2socks card, for servers with no browser.
func newTun2SocksCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "tun2socks",
		Short: "Manage the external tun2socks binary used for whole-OS TUN capture",
		Long: "TUN capture runs the official tun2socks binary (https://tun2socks.com)\n" +
			"as a separate process, so only it needs root/Administrator - not the\n" +
			"filtering engine. These subcommands install it and report its state.",
	}

	download := &cobra.Command{
		Use:   "download",
		Short: "Download the tun2socks binary and install it beside webfilter",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTun2SocksDownload(cmd.Context())
		},
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "Report tun2socks configuration, binary, and privilege state",
	}
	f := addConfigFlags(status)
	status.RunE = func(cmd *cobra.Command, args []string) error {
		return runTun2SocksStatus(f.settingsPath)
	}

	root.AddCommand(download, status)
	return root
}

func runTun2SocksDownload(ctx context.Context) error {
	dir, err := tun.InstallDir()
	if err != nil {
		return fmt.Errorf("locate install directory: %w", err)
	}
	fmt.Printf("[tun2socks] downloading %s into %s ...\n", tun.AssetName(runtime.GOOS, runtime.GOARCH), dir)

	meta, err := tun.Download(ctx, dir, "")
	if err != nil {
		return err
	}
	fmt.Printf("[tun2socks] installed %s (sha256 %s)\n", meta.Version, meta.SHA256)
	return nil
}

func runTun2SocksStatus(settingsPath string) error {
	settings, err := config.LoadSettings(settingsPath)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	st := tun.Inspect(settings)

	fmt.Printf("platform:   %s (supported: %t)\n", st.Platform, st.Supported)
	fmt.Printf("enabled:    %t\n", st.Enabled)
	fmt.Printf("device:     %s\n", st.DeviceName)
	fmt.Printf("privilege:  %s (ok: %t)\n", st.Privilege, st.PrivilegeOK)
	if st.BinaryPresent {
		fmt.Printf("binary:     %s (%s)\n", st.BinaryPath, st.BinarySource)
		if st.BinaryVersion != "" {
			fmt.Printf("version:    %s\n", st.BinaryVersion)
		}
		if st.Downloaded != "" {
			fmt.Printf("downloaded: %s\n", st.Downloaded)
		}
	} else {
		fmt.Println("binary:     not installed (run `webfilter tun2socks download`)")
	}
	if st.LastError != "" {
		fmt.Printf("problem:    %s\n", st.LastError)
	}
	return nil
}
