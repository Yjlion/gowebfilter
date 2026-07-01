package main

import (
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run both the proxy engine and the management server in one process",
	}
	f := addConfigFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runProxyAndMgmt(cmd, f.settingsPath)
	}
	return cmd
}
