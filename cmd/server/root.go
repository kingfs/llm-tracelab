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
	cmd.SetArgs(normalizeRootArgs(args))
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
	cmd := &cobra.Command{
		Use:           cliName,
		Short:         "Local-first LLM API record/replay proxy",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			printUsage(cmd.ErrOrStderr())
			return cliExitError{code: 2}
		},
	}
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

func normalizeRootArgs(args []string) []string {
	if len(args) == 0 {
		return []string{"serve"}
	}
	switch args[0] {
	case "-c", "--config":
		return append([]string{"serve"}, args...)
	default:
		return args
	}
}

func commandAdapter(use string, short string, run func([]string) int) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if code := run(args); code != 0 {
				return cliExitError{code: code}
			}
			return nil
		},
	}
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
