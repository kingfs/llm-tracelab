package main

import "github.com/spf13/cobra"

func newDBCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "db",
		Short:         "Manage application database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	migrateCmd := &cobra.Command{
		Use:           "migrate",
		Short:         "Apply or roll back application database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	migrateCmd.AddCommand(newAuthMigrateDirectionCommand(runtime, "up", "Apply application database migrations"))
	migrateCmd.AddCommand(newAuthMigrateDirectionCommand(runtime, "down", "Roll back application database migrations"))
	cmd.AddCommand(migrateCmd)
	return cmd
}
