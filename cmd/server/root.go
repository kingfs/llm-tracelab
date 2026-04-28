package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const cliName = "llm-tracelab"

func run(args []string) int {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cmd := newRootCommand()
	cmd.SetArgs(normalizeLegacyFlagArgs(args))
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
	runtime := newCLIRuntime()
	cmd := &cobra.Command{
		Use:           cliName,
		Short:         "Local-first LLM API record/replay proxy",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runServeWithConfig(runtime.configPath())
			})
		},
	}
	cmd.PersistentFlags().StringP("config", "c", "config.yaml", "Path to configuration file")
	mustBindPFlag(runtime.settings, "config", cmd.PersistentFlags().Lookup("config"))
	cmd.AddCommand(
		newServeCommand(runtime),
		newMigrateCommand(runtime),
		newDBCommand(runtime),
		newAuthCommand(runtime),
		newVersionCommand(),
		newCompletionCommand(cmd),
	)
	return cmd
}

type cliRuntime struct {
	settings *viper.Viper
}

func newCLIRuntime() *cliRuntime {
	settings := viper.New()
	settings.SetEnvPrefix("LLM_TRACELAB")
	settings.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	settings.AutomaticEnv()
	return &cliRuntime{settings: settings}
}

func (rt *cliRuntime) configPath() string {
	if rt == nil || rt.settings == nil {
		return "config.yaml"
	}
	if path := rt.settings.GetString("config"); path != "" {
		return path
	}
	return "config.yaml"
}

func runCode(run func() int) error {
	if code := run(); code != 0 {
		return cliExitError{code: code}
	}
	return nil
}

func requireSubcommand(cmd *cobra.Command) error {
	_ = cmd.Help()
	return cliExitError{code: 2}
}

func normalizeLegacyFlagArgs(args []string) []string {
	normalized := make([]string, len(args))
	for i, arg := range args {
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && len(arg) > 2 {
			normalized[i] = "-" + arg
			continue
		}
		normalized[i] = arg
	}
	return normalized
}

func mustBindPFlag(settings *viper.Viper, key string, flag *pflag.Flag) {
	if err := settings.BindPFlag(key, flag); err != nil {
		panic(err)
	}
}

type cliExitError struct {
	code int
}

func (e cliExitError) Error() string {
	return fmt.Sprintf("exit %d", e.code)
}
