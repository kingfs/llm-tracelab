package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

const cliName = "llm-tracelab"

func run(args []string) int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cmd := newRootCommand()
	cmd.SetArgs(args)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	if err := cmd.Execute(); err != nil {
		var exit cliExitError
		if errors.As(err, &exit) {
			return exit.code
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func newRootCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:           cliName,
		Short:         "Local-first LLM API record/replay proxy",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if code := runServe([]string{"-c", configPath}); code != 0 {
				return cliExitError{code: code}
			}
			return nil
		},
	}
	cmd.PersistentFlags().StringVarP(&configPath, "config", "c", "config.yaml", "Path to configuration file")
	cmd.AddCommand(
		newServeCommand(),
		newMigrateCommand(),
		newDBCommand(),
		newAuthCommand(),
		newVersionCommand(),
		newCompletionCommand(cmd),
	)
	return cmd
}

func runCode(run func([]string) int, args []string) error {
	if code := run(args); code != 0 {
		return cliExitError{code: code}
	}
	return nil
}

func configArg(cmd *cobra.Command) []string {
	configPath, err := cmd.Root().PersistentFlags().GetString("config")
	if err != nil || configPath == "" {
		configPath = "config.yaml"
	}
	return []string{"-c", configPath}
}

type cliExitError struct {
	code int
}

func (e cliExitError) Error() string {
	return fmt.Sprintf("exit %d", e.code)
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  llm-tracelab serve -c config.yaml")
	fmt.Fprintln(w, "  llm-tracelab migrate -c config.yaml [-rewrite-v2=true] [-rebuild-index=true]")
	fmt.Fprintln(w, "  llm-tracelab db migrate up|down -c config.yaml")
	fmt.Fprintln(w, "  llm-tracelab auth migrate up|down -c config.yaml")
	fmt.Fprintln(w, "  llm-tracelab auth init-user -c config.yaml --username admin --password 'change-me'")
	fmt.Fprintln(w, "  llm-tracelab auth reset-password -c config.yaml --username admin --password 'new-password'")
	fmt.Fprintln(w, "  llm-tracelab auth create-token -c config.yaml --username admin --name cli")
	fmt.Fprintln(w, "  llm-tracelab version")
	fmt.Fprintln(w, "  llm-tracelab completion bash|zsh|fish|powershell")
}
