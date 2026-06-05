package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
	Branch  = "unknown"
)

type versionInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Branch  string `json:"branch"`
}

func newVersionCommand(runtime *cliRuntime) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := versionInfo{
				Name:    cliName,
				Version: Version,
				Commit:  Commit,
				Date:    Date,
				Branch:  Branch,
			}
			return writeCLIResult(cmd.OutOrStdout(), runtime.outputFormat(), "version", info, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "%s version=%s commit=%s date=%s branch=%s\n", cliName, Version, Commit, Date, Branch)
				return err
			})
		},
	}
}
