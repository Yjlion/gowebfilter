package main

import "github.com/spf13/cobra"

func newOuiCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "oui",
		Short: "Manage the embedded IEEE OUI vendor lookup table",
	}
	update := &cobra.Command{
		Use:   "update",
		Short: "Refresh the OUI vendor lookup table",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("oui update")
		},
	}
	root.AddCommand(update)
	return root
}
