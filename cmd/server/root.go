package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const cliName = "llm-tracelab"

func run(args []string) int {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	runtime := newCLIRuntime()
	cmd := newRootCommandWithRuntime(runtime)
	cmd.SetArgs(normalizeLegacyFlagArgs(args))
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	if err := cmd.Execute(); err != nil {
		var exit cliExitError
		if errors.As(err, &exit) {
			writeCLIError(cmd.ErrOrStderr(), runtime.outputFormat(), exit)
			return exit.code
		}
		exit = cliExitError{
			code:     exitCodeInternal,
			category: errorCategoryInternal,
			errCode:  "INTERNAL_ERROR",
			message:  err.Error(),
		}
		writeCLIError(cmd.ErrOrStderr(), runtime.outputFormat(), exit)
		return 1
	}
	return 0
}

func newRootCommand() *cobra.Command {
	return newRootCommandWithRuntime(newCLIRuntime())
}

func newRootCommandWithRuntime(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           cliName,
		Short:         "Local-first LLM API record/replay proxy",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return runtime.validateOutputFormat()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runServeWithConfig(runtime.configPath())
			})
		},
	}
	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return cliUsageError(err.Error(), "flag")
	})
	cmd.PersistentFlags().StringP("config", "c", "config.yaml", "Path to configuration file")
	cmd.PersistentFlags().String("format", "text", "Output format: text or json")
	cmd.PersistentFlags().Bool("no-input", true, "Fail instead of prompting for input; this CLI is non-interactive by default")
	mustBindPFlag(runtime.settings, "config", cmd.PersistentFlags().Lookup("config"))
	mustBindPFlag(runtime.settings, "format", cmd.PersistentFlags().Lookup("format"))
	mustBindPFlag(runtime.settings, "no-input", cmd.PersistentFlags().Lookup("no-input"))
	cmd.AddCommand(
		newServeCommand(runtime),
		newMigrateCommand(runtime),
		newDBCommand(runtime),
		newAuthCommand(runtime),
		newVersionCommand(runtime),
		newSchemaCommand(runtime, cmd),
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

func (rt *cliRuntime) outputFormat() string {
	if rt == nil || rt.settings == nil {
		return "text"
	}
	if format := rt.settings.GetString("format"); format != "" {
		return format
	}
	return "text"
}

func (rt *cliRuntime) validateOutputFormat() error {
	switch rt.outputFormat() {
	case "text", "json":
		return nil
	default:
		return cliExitError{
			code:     exitCodeUsage,
			category: errorCategoryUsage,
			errCode:  "INVALID_OUTPUT_FORMAT",
			message:  "--format must be one of: text, json",
			field:    "format",
		}
	}
}

func runCode(run func() int) error {
	if code := run(); code != 0 {
		return cliExitErrorFromCode(code)
	}
	return nil
}

func cliUsageError(message string, field string) cliExitError {
	return cliExitError{
		code:     exitCodeUsage,
		category: errorCategoryUsage,
		errCode:  "USAGE_ERROR",
		message:  message,
		field:    field,
	}
}

func requireSubcommand(cmd *cobra.Command) error {
	if cmd.Flag("format") != nil && cmd.Flag("format").Value.String() == "json" {
		return cliUsageError(fmt.Sprintf("%s requires a subcommand", cmd.CommandPath()), "command")
	}
	if err := cmd.Help(); err != nil {
		return cliExitError{
			code:     exitCodeInternal,
			category: errorCategoryInternal,
			errCode:  "HELP_RENDER_FAILED",
			message:  err.Error(),
		}
	}
	return cliExitErrorFromCode(exitCodeUsage)
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
	code     int
	category string
	errCode  string
	message  string
	field    string
}

func (e cliExitError) Error() string {
	if e.message != "" {
		return e.message
	}
	return fmt.Sprintf("exit %d", e.code)
}

const (
	exitCodeAPI           = 1
	exitCodeUsage         = 3
	exitCodeInternal      = 8
	errorCategoryAPI      = "api"
	errorCategoryUsage    = "usage"
	errorCategoryInternal = "internal"
)

func cliExitErrorFromCode(code int) cliExitError {
	switch code {
	case 2, exitCodeUsage:
		return cliExitError{
			code:     exitCodeUsage,
			category: errorCategoryUsage,
			errCode:  "USAGE_ERROR",
			message:  "command usage is invalid",
		}
	case exitCodeInternal:
		return cliExitError{
			code:     exitCodeInternal,
			category: errorCategoryInternal,
			errCode:  "INTERNAL_ERROR",
			message:  "command failed with an internal error",
		}
	default:
		return cliExitError{
			code:     code,
			category: errorCategoryAPI,
			errCode:  "COMMAND_FAILED",
			message:  "command failed",
		}
	}
}

type cliEnvelope struct {
	OK       bool      `json:"ok"`
	Command  string    `json:"command,omitempty"`
	Result   any       `json:"result,omitempty"`
	Warnings []string  `json:"warnings"`
	Error    *cliError `json:"error,omitempty"`
}

type cliError struct {
	Code        string   `json:"code"`
	Category    string   `json:"category"`
	Message     string   `json:"message"`
	Field       string   `json:"field,omitempty"`
	Retryable   bool     `json:"retryable"`
	SafeToRetry bool     `json:"safe_to_retry"`
	Suggestions []string `json:"suggested_commands,omitempty"`
}

func writeCLIResult(w io.Writer, format string, command string, result any, text func(io.Writer) error) error {
	if format != "json" {
		if text == nil {
			return nil
		}
		return text(w)
	}
	return writeJSON(w, cliEnvelope{
		OK:       true,
		Command:  command,
		Result:   result,
		Warnings: []string{},
	})
}

func writeCLIError(w io.Writer, format string, exit cliExitError) {
	if format != "json" {
		if exit.message != "" {
			fmt.Fprintln(w, exit.message)
		}
		return
	}
	_ = writeJSON(w, cliEnvelope{
		OK:       false,
		Warnings: []string{},
		Error: &cliError{
			Code:        fallback(exit.errCode, "COMMAND_FAILED"),
			Category:    fallback(exit.category, errorCategoryAPI),
			Message:     fallback(exit.message, "command failed"),
			Field:       exit.field,
			Retryable:   exit.category != errorCategoryUsage,
			SafeToRetry: exit.category != errorCategoryUsage,
		},
	})
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func fallback(value string, fallbackValue string) string {
	if value != "" {
		return value
	}
	return fallbackValue
}
