package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type cliSchema struct {
	Name        string             `json:"name"`
	Version     string             `json:"version"`
	Description string             `json:"description"`
	Contracts   cliSchemaContracts `json:"contracts"`
	Commands    []cliCommandSchema `json:"commands"`
}

type cliSchemaContracts struct {
	Formats       []string         `json:"formats"`
	ExitCodes     map[string]int   `json:"exit_codes"`
	Stdout        string           `json:"stdout"`
	Stderr        string           `json:"stderr"`
	ErrorEnvelope cliErrorEnvelope `json:"error_envelope"`
}

type cliErrorEnvelope struct {
	OK    bool     `json:"ok"`
	Error cliError `json:"error"`
}

type cliCommandSchema struct {
	Path        string          `json:"path"`
	Use         string          `json:"use"`
	Short       string          `json:"short"`
	Args        string          `json:"args,omitempty"`
	Flags       []cliFlagSchema `json:"flags,omitempty"`
	Subcommands []string        `json:"subcommands,omitempty"`
}

type cliFlagSchema struct {
	Name      string   `json:"name"`
	Shorthand string   `json:"shorthand,omitempty"`
	Usage     string   `json:"usage"`
	Default   string   `json:"default,omitempty"`
	Type      string   `json:"type"`
	Required  bool     `json:"required"`
	Values    []string `json:"values,omitempty"`
}

func newSchemaCommand(runtime *cliRuntime, root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "schema [command...]",
		Short: "Print machine-readable CLI schema for agents and automation",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := root
			if len(args) > 0 {
				var err error
				target, err = findCommandByPath(root, strings.Join(args, " "))
				if err != nil {
					return err
				}
			}
			schema := buildCLISchema(root, target)
			return writeCLIResult(cmd.OutOrStdout(), runtime.outputFormat(), "schema", schema, func(w io.Writer) error {
				for _, item := range schema.Commands {
					if _, err := fmt.Fprintf(w, "%s\t%s\n", item.Path, item.Short); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}

func findCommandByPath(root *cobra.Command, path string) (*cobra.Command, error) {
	parts := strings.Fields(path)
	if len(parts) == 0 {
		return root, nil
	}
	if parts[0] == root.Name() {
		parts = parts[1:]
	}
	target, _, err := root.Find(parts)
	if err != nil || target == nil {
		return nil, cliExitError{
			code:     exitCodeUsage,
			category: errorCategoryUsage,
			errCode:  "UNKNOWN_COMMAND",
			message:  fmt.Sprintf("unknown command %q", path),
			field:    "command",
		}
	}
	return target, nil
}

func buildCLISchema(root *cobra.Command, target *cobra.Command) cliSchema {
	return cliSchema{
		Name:        cliName,
		Version:     Version,
		Description: root.Short,
		Contracts: cliSchemaContracts{
			Formats: []string{"text", "json"},
			ExitCodes: map[string]int{
				"success":             0,
				"api_error":           exitCodeAPI,
				"usage_or_validation": exitCodeUsage,
				"internal_error":      exitCodeInternal,
			},
			Stdout: "primary command result only",
			Stderr: "logs, diagnostics, and structured error envelopes on failure",
			ErrorEnvelope: cliErrorEnvelope{
				OK: false,
				Error: cliError{
					Code:        "USAGE_ERROR",
					Category:    errorCategoryUsage,
					Message:     "example error message",
					Field:       "flag_or_argument",
					Retryable:   false,
					SafeToRetry: false,
				},
			},
		},
		Commands: collectCommandSchemas(root, target),
	}
}

func collectCommandSchemas(root *cobra.Command, target *cobra.Command) []cliCommandSchema {
	var schemas []cliCommandSchema
	if target == root {
		walkCommands(root, func(cmd *cobra.Command) {
			schemas = append(schemas, buildCommandSchema(cmd))
		})
		return schemas
	}
	schemas = append(schemas, buildCommandSchema(target))
	return schemas
}

func walkCommands(cmd *cobra.Command, visit func(*cobra.Command)) {
	if cmd.Hidden {
		return
	}
	visit(cmd)
	for _, child := range cmd.Commands() {
		walkCommands(child, visit)
	}
}

func buildCommandSchema(cmd *cobra.Command) cliCommandSchema {
	item := cliCommandSchema{
		Path:  cmd.CommandPath(),
		Use:   cmd.Use,
		Short: cmd.Short,
		Flags: collectFlagSchemas(cmd),
	}
	for _, child := range cmd.Commands() {
		if !child.Hidden {
			item.Subcommands = append(item.Subcommands, child.CommandPath())
		}
	}
	return item
}

func collectFlagSchemas(cmd *cobra.Command) []cliFlagSchema {
	var flags []cliFlagSchema
	seen := map[string]bool{}
	addFlagSet := func(flagSet *pflag.FlagSet) {
		flagSet.VisitAll(func(flag *pflag.Flag) {
			if seen[flag.Name] {
				return
			}
			seen[flag.Name] = true
			flags = append(flags, cliFlagSchema{
				Name:      flag.Name,
				Shorthand: flag.Shorthand,
				Usage:     flag.Usage,
				Default:   flag.DefValue,
				Type:      flag.Value.Type(),
				Required:  isRequiredFlag(flag),
				Values:    knownFlagValues(flag),
			})
		})
	}
	addFlagSet(cmd.InheritedFlags())
	addFlagSet(cmd.PersistentFlags())
	addFlagSet(cmd.Flags())
	return flags
}

func isRequiredFlag(flag *pflag.Flag) bool {
	if flag == nil || flag.Annotations == nil {
		return false
	}
	values := flag.Annotations[cobra.BashCompOneRequiredFlag]
	return len(values) > 0 && values[0] == "true"
}

func knownFlagValues(flag *pflag.Flag) []string {
	if flag == nil || flag.Name != "format" {
		return nil
	}
	return []string{"text", "json"}
}
