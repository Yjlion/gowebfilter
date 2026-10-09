package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/yjlion/gowebfilter/internal/adblock"
	"github.com/yjlion/gowebfilter/internal/config"
	"github.com/yjlion/gowebfilter/internal/proxy"
)

func newAdblockCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "adblock",
		Short: "Manage the shared ad/tracker filter lists",
	}

	update := &cobra.Command{
		Use:   "update [list...]",
		Short: "Download or refresh filter lists (default: every installed list)",
	}
	uf := addConfigFlags(update)
	update.RunE = func(cmd *cobra.Command, args []string) error {
		return runAdblockUpdate(cmd.Context(), uf.settingsPath, args)
	}

	status := &cobra.Command{
		Use:   "status",
		Short: "List known filter lists and their install state",
	}
	sf := addConfigFlags(status)
	status.RunE = func(cmd *cobra.Command, args []string) error {
		st, err := adblockStoreFor(sf.settingsPath)
		if err != nil {
			return err
		}
		fmt.Printf("lists in %s\n", st.Dir())
		for _, m := range st.Status() {
			state := "not installed"
			if m.Installed {
				state = fmt.Sprintf("%d rules, updated %s", m.Rules, m.Updated)
			}
			if m.Error != "" {
				state += " (last error: " + m.Error + ")"
			}
			fmt.Printf("  %-18s %s\n", m.Name, state)
		}
		return nil
	}

	root.AddCommand(update, status)
	return root
}

func adblockStoreFor(settingsPath string) (*adblock.Store, error) {
	settings, err := config.LoadSettings(settingsPath)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	st := adblock.NewStoreFromSettings(settings.Adblock)
	st.Client = &http.Client{Transport: proxy.NewTransport(), Timeout: 5 * time.Minute}
	return st, nil
}

func runAdblockUpdate(ctx context.Context, settingsPath string, names []string) error {
	st, err := adblockStoreFor(settingsPath)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		for _, m := range st.Status() {
			if m.Installed {
				names = append(names, m.Name)
			}
		}
		if len(names) == 0 {
			names = adblock.DefaultLists
		}
	}
	var failed []string
	for _, n := range names {
		meta, err := st.Download(ctx, n)
		if err != nil {
			fmt.Printf("[adblock] %-18s FAILED: %v\n", n, err)
			failed = append(failed, n)
			continue
		}
		fmt.Printf("[adblock] %-18s %8d rules\n", n, meta.Rules)
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed: %s", strings.Join(failed, ", "))
	}
	return nil
}
