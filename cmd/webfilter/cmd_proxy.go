package main

import "github.com/spf13/cobra"

func newProxyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Run only the MITM filtering proxy engine",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("proxy")
		},
	}
	addConfigFlags(cmd)
	return cmd
}
