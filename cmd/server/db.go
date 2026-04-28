package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newDBCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "db",
		Short:         "Manage application database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.ErrOrStderr(), "db requires migrate up or migrate down")
			return cliExitError{code: 2}
		},
	}
	migrateCmd := &cobra.Command{
		Use:           "migrate",
		Short:         "Apply or roll back application database migrations",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.ErrOrStderr(), "db migrate requires up or down")
			return cliExitError{code: 2}
		},
	}
	migrateCmd.AddCommand(commandAdapter("up", "Apply application database migrations", func(args []string) int {
		return runAuthMigrate(append([]string{"up"}, args...))
	}))
	migrateCmd.AddCommand(commandAdapter("down", "Roll back application database migrations", func(args []string) int {
		return runAuthMigrate(append([]string{"down"}, args...))
	}))
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
