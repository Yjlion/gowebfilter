package main

import "github.com/spf13/cobra"

func newCategoriesCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "categories",
		Short: "Manage shared site-category blocklists",
	}
	update := &cobra.Command{
		Use:   "update",
		Short: "Download and refresh site-category blocklists",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errNotImplemented("categories update")
		},
	}
	root.AddCommand(update)
	return root
}
