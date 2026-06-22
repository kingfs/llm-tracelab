package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/channel"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/providerprobe"
	"github.com/spf13/cobra"
)

type providerProbeOptions struct {
	configPath string
	format     string
	stdout     io.Writer
	id         string
	timeout    time.Duration
	command    string
}

func newProviderCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "provider",
		Short:         "Inspect configured upstream providers",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	cmd.AddCommand(newProviderProbeCommand(runtime))
	cmd.AddCommand(newProviderProbeReportCommand(runtime))
	cmd.AddCommand(newProviderProbeApplyCommand(runtime))
	return cmd
}

func newProviderProbeCommand(runtime *cliRuntime) *cobra.Command {
	var id string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "probe",
		Short: "Probe configured upstream provider API surfaces",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runProviderProbeWithOptions(providerProbeOptions{
					configPath: runtime.configPath(),
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
					id:         id,
					timeout:    timeout,
					command:    "provider.probe",
				})
			})
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Probe only the upstream target with this id")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "HTTP timeout for each probe request")
	return cmd
}

func newProviderProbeApplyCommand(runtime *cliRuntime) *cobra.Command {
	var id string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "probe-apply",
		Short: "Apply provider probe suggestions to managed channels",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runProviderProbeApplyWithOptions(providerProbeOptions{
					configPath: runtime.configPath(),
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
					id:         id,
					timeout:    timeout,
					command:    "provider.probe_apply",
				})
			})
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Apply suggestions only to the channel with this id")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "HTTP timeout for each probe request")
	return cmd
}

func newProviderProbeReportCommand(runtime *cliRuntime) *cobra.Command {
	var id string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "probe-report",
		Short: "Generate a read-only batch provider probe report for configured upstreams",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runProviderProbeWithOptions(providerProbeOptions{
					configPath: runtime.configPath(),
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
					id:         id,
					timeout:    timeout,
					command:    "provider.probe_report",
				})
			})
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "Report only the upstream target with this id")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "HTTP timeout for each probe request")
	return cmd
}

func runProviderProbeWithOptions(opts providerProbeOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	targets, err := providerProbeTargets(*cfg, opts.id)
	if err != nil {
		slog.Error("Provider probe target selection failed", "error", err)
		return 2
	}
	timeout := opts.timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	ctx := context.Background()
	result := providerprobe.ProbeBatch(ctx, targets, client)
	for _, report := range result.Reports {
		if report.Status == providerprobe.StatusError && report.Error != "" {
			slog.Error("Provider probe failed", "provider_id", report.ProviderID, "error", report.Error)
		}
	}
	command := strings.TrimSpace(opts.command)
	if command == "" {
		command = "provider.probe"
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, command, result, func(w io.Writer) error {
		writeProviderProbeText(w, result)
		return nil
	}); err != nil {
		slog.Error("Write provider probe result failed", "error", err)
		return 1
	}
	return 0
}

func providerProbeTargets(cfg config.Config, id string) ([]providerprobe.ProbeTarget, error) {
	id = strings.TrimSpace(id)
	configured := cfg.EffectiveUpstreams()
	targets := make([]providerprobe.ProbeTarget, 0, len(configured))
	for _, target := range configured {
		if target.Enabled != nil && !*target.Enabled {
			continue
		}
		targetID := strings.TrimSpace(target.ID)
		if id != "" && targetID != id {
			continue
		}
		probeTarget := providerProbeTargetFromConfig(target)
		probeTarget.TargetSource = "upstream"
		if strings.TrimSpace(probeTarget.BaseURL) == "" {
			continue
		}
		targets = append(targets, probeTarget)
	}
	if id != "" && len(targets) == 0 {
		return nil, fmt.Errorf("upstream target %q was not found or has no base_url", id)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no enabled upstream provider with base_url is configured")
	}
	return targets, nil
}

func providerProbeTargetFromConfig(target config.UpstreamTargetConfig) providerprobe.ProbeTarget {
	upstream := target.Upstream
	apiKey := strings.TrimSpace(upstream.ApiKey)
	headers := cloneProviderProbeHeaders(upstream.Headers)
	for _, credential := range target.EffectiveCredentials() {
		if credential.Enabled != nil && !*credential.Enabled {
			continue
		}
		if apiKey == "" {
			apiKey = strings.TrimSpace(credential.ApiKey)
		}
		for key, value := range credential.Headers {
			headers[key] = value
		}
		break
	}
	return providerprobe.ProbeTarget{
		ProviderID:              strings.TrimSpace(target.ID),
		BaseURL:                 strings.TrimSpace(upstream.BaseURL),
		APIKey:                  apiKey,
		Headers:                 headers,
		SpecifiedAPIType:        upstream.APIType,
		SpecifiedProtocolFamily: upstream.ProtocolFamily,
	}
}

func cloneProviderProbeHeaders(in map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func runProviderProbeApplyWithOptions(opts providerProbeOptions) int {
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	st, err := openApplicationDatabase(cfg)
	if err != nil {
		slog.Error("Open application store failed", "error", err)
		return 1
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Error("Close application store failed", "error", err)
		}
	}()

	timeout := opts.timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	svc := channel.NewService(st).WithHTTPClient(&http.Client{Timeout: timeout})
	result, err := svc.ApplyProviderProbeReport(context.Background(), channel.ProviderProbeReportOptions{
		ChannelID: strings.TrimSpace(opts.id),
	})
	if err != nil {
		slog.Error("Provider probe apply failed", "error", err)
		return 2
	}
	command := strings.TrimSpace(opts.command)
	if command == "" {
		command = "provider.probe_apply"
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, command, result, func(w io.Writer) error {
		writeProviderProbeApplyText(w, result)
		return nil
	}); err != nil {
		slog.Error("Write provider probe apply result failed", "error", err)
		return 1
	}
	return 0
}

func writeProviderProbeText(w io.Writer, result providerprobe.BatchReport) {
	for _, report := range result.Reports {
		id := report.ProviderID
		if id == "" {
			id = "default"
		}
		if report.TargetSource != "" {
			fmt.Fprintf(w, "source: %s\n", report.TargetSource)
		}
		fmt.Fprintf(w, "provider: %s\n", id)
		fmt.Fprintf(w, "base_url: %s\n", report.BaseURL)
		fmt.Fprintf(w, "status: %s\n", report.Status)
		if report.SuggestedProtocolFamily != "" {
			fmt.Fprintf(w, "suggested_protocol_family: %s\n", report.SuggestedProtocolFamily)
		}
		if report.SuggestedAPIType != "" {
			fmt.Fprintf(w, "suggested_api_type: %s\n", report.SuggestedAPIType)
		}
		if len(report.Capabilities) > 0 {
			fmt.Fprintf(w, "capabilities: %s\n", strings.Join(report.Capabilities, ","))
		}
		if len(report.Warnings) > 0 {
			fmt.Fprintf(w, "warnings: %s\n", strings.Join(report.Warnings, "; "))
		}
		fmt.Fprintln(w)
	}
}

func writeProviderProbeApplyText(w io.Writer, result channel.ProviderProbeApplyResult) {
	fmt.Fprintln(w, "provider probe report")
	writeProviderProbeText(w, result.Report)
	if len(result.Applied) == 0 {
		fmt.Fprintln(w, "applied: none")
		return
	}
	fmt.Fprintln(w, "channel apply results")
	for _, item := range result.Applied {
		channelID := item.ChannelID
		if channelID == "" {
			channelID = "default"
		}
		fmt.Fprintf(w, "channel: %s\n", channelID)
		fmt.Fprintf(w, "status: %s\n", item.Status)
		fmt.Fprintf(w, "applied: %t\n", item.Applied)
		if len(item.AppliedFields) > 0 {
			fmt.Fprintf(w, "applied_fields: %s\n", strings.Join(item.AppliedFields, ","))
		}
		if item.SkippedReason != "" {
			fmt.Fprintf(w, "skipped_reason: %s\n", item.SkippedReason)
		}
		fmt.Fprintln(w)
	}
}
