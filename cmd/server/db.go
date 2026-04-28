package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

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

func runDB(args []string) int {
	if len(args) == 0 || args[0] != "migrate" {
		fmt.Fprintln(os.Stderr, "db requires migrate up or migrate down")
		return 2
	}
	return runAuthMigrate(args[1:])
}
