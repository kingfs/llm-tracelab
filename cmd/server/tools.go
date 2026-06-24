package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"

	appconfig "github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/responses/tools/hosted"
	"github.com/kingfs/llm-tracelab/internal/responses/tools/websearch"
	"github.com/spf13/cobra"
)

const (
	toolsReadinessReady    = "ready"
	toolsReadinessNotReady = "not_ready"
	toolsReadinessDisabled = "disabled"
)

type toolsStatusOptions struct {
	configPath string
	format     string
	stdout     io.Writer
}

type toolsStatusResult struct {
	ConfigPath                string                     `json:"config_path"`
	ResponsesServer           toolsStatusResponsesServer `json:"responses_server"`
	Tools                     toolsStatusTools           `json:"tools"`
	ReadyForCodexWebSearch    bool                       `json:"ready_for_codex_web_search"`
	ReadyForCodexWebSearchWhy string                     `json:"ready_for_codex_web_search_reason"`
	Warnings                  []string                   `json:"warnings,omitempty"`
}

type toolsStatusResponsesServer struct {
	Enabled     bool                   `json:"enabled"`
	CodexCompat toolsStatusCodexCompat `json:"codex_compat"`
}

type toolsStatusCodexCompat struct {
	Enabled               bool     `json:"enabled"`
	AutoInjectHostedTools []string `json:"auto_inject_hosted_tools"`
	InjectableTools       []string `json:"injectable_tools"`
}

type toolsStatusTools struct {
	WebSearch toolsStatusWebSearch `json:"web_search"`
	MCP       toolsStatusMCP       `json:"mcp"`
}

type toolsStatusWebSearch struct {
	Enabled        bool     `json:"enabled"`
	Provider       string   `json:"provider"`
	BaseURLPresent bool     `json:"base_url_present"`
	MaxResults     int      `json:"max_results"`
	TimeoutMS      int      `json:"timeout_ms"`
	Readiness      string   `json:"readiness"`
	Warnings       []string `json:"warnings,omitempty"`
}

type toolsStatusMCP struct {
	Enabled            bool                   `json:"enabled"`
	ServerCount        int                    `json:"server_count"`
	EnabledServerCount int                    `json:"enabled_server_count"`
	Readiness          string                 `json:"readiness"`
	Warnings           []string               `json:"warnings,omitempty"`
	Servers            []toolsStatusMCPServer `json:"servers,omitempty"`
}

type toolsStatusMCPServer struct {
	ID                    string   `json:"id,omitempty"`
	Label                 string   `json:"label,omitempty"`
	URL                   string   `json:"url,omitempty"`
	BearerTokenEnv        string   `json:"bearer_token_env,omitempty"`
	BearerTokenConfigured bool     `json:"bearer_token_configured"`
	EnabledTools          []string `json:"enabled_tools,omitempty"`
	DisabledTools         []string `json:"disabled_tools,omitempty"`
	Enabled               bool     `json:"enabled"`
}

func newToolsCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "tools",
		Short:         "Inspect hosted tool readiness",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	cmd.AddCommand(newToolsStatusCommand(runtime))
	return cmd
}

func newToolsStatusCommand(runtime *cliRuntime) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print offline hosted tool readiness for integration tests",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runToolsStatusWithOptions(toolsStatusOptions{
					configPath: runtime.configPath(),
					format:     runtime.outputFormat(),
					stdout:     cmd.OutOrStdout(),
				})
			})
		},
	}
}

func runToolsStatusWithOptions(opts toolsStatusOptions) int {
	cfg, err := appconfig.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	result := buildToolsStatusResult(opts.configPath, cfg)
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "tools.status", result, func(w io.Writer) error {
		writeToolsStatusText(w, result)
		return nil
	}); err != nil {
		slog.Error("Write tools status result failed", "error", err)
		return 1
	}
	return 0
}

func buildToolsStatusResult(configPath string, cfg *appconfig.Config) toolsStatusResult {
	if cfg == nil {
		cfg = &appconfig.Config{}
	}
	compat := cfg.ResponsesCodexCompatConfig()
	webSearch := buildToolsStatusWebSearch(cfg.WebSearchConfig())
	mcp := buildToolsStatusMCP(cfg.MCPToolsConfig())
	injectable := toolsStatusInjectableTools(webSearch, mcp)

	result := toolsStatusResult{
		ConfigPath: configPath,
		ResponsesServer: toolsStatusResponsesServer{
			Enabled: cfg.ResponsesServerEnabled(),
			CodexCompat: toolsStatusCodexCompat{
				Enabled:               compat.Enabled,
				AutoInjectHostedTools: append([]string(nil), compat.AutoInjectHostedTools...),
				InjectableTools:       injectable,
			},
		},
		Tools: toolsStatusTools{
			WebSearch: webSearch,
			MCP:       mcp,
		},
	}
	result.ReadyForCodexWebSearch, result.ReadyForCodexWebSearchWhy = toolsStatusCodexWebSearchReadiness(result)
	result.Warnings = append(result.Warnings, webSearch.Warnings...)
	result.Warnings = append(result.Warnings, mcp.Warnings...)
	if !result.ReadyForCodexWebSearch {
		result.Warnings = append(result.Warnings, result.ReadyForCodexWebSearchWhy)
	}
	return result
}

func buildToolsStatusWebSearch(cfg appconfig.WebSearchToolConfig) toolsStatusWebSearch {
	status := toolsStatusWebSearch{
		Enabled:        cfg.Enabled,
		Provider:       strings.TrimSpace(cfg.Provider),
		BaseURLPresent: strings.TrimSpace(cfg.BaseURL) != "",
		MaxResults:     cfg.MaxResults,
		TimeoutMS:      cfg.TimeoutMS,
		Readiness:      toolsReadinessDisabled,
	}
	if !cfg.Enabled {
		return status
	}
	if strings.EqualFold(cfg.Provider, websearch.ProviderDisabled) {
		status.Readiness = toolsReadinessNotReady
		status.Warnings = append(status.Warnings, "tools.web_search.enabled is true but provider is disabled")
		return status
	}
	if _, err := websearch.NewProvider(websearch.Options{
		Provider:   cfg.Provider,
		BaseURL:    cfg.BaseURL,
		TimeoutMS:  cfg.TimeoutMS,
		UserAgent:  cfg.UserAgent,
		MaxResults: cfg.MaxResults,
	}); err != nil {
		status.Readiness = toolsReadinessNotReady
		status.Warnings = append(status.Warnings, redactDoctorMessage(err.Error()))
		return status
	}
	status.Readiness = toolsReadinessReady
	return status
}

func buildToolsStatusMCP(cfg appconfig.MCPToolConfig) toolsStatusMCP {
	status := toolsStatusMCP{
		Enabled:     cfg.Enabled,
		ServerCount: len(cfg.Servers),
		Readiness:   toolsReadinessDisabled,
		Servers:     make([]toolsStatusMCPServer, 0, len(cfg.Servers)),
	}
	for _, server := range cfg.Servers {
		enabled := server.EnabledOrDefault()
		if enabled {
			status.EnabledServerCount++
		}
		envName := strings.TrimSpace(server.BearerTokenEnv)
		status.Servers = append(status.Servers, toolsStatusMCPServer{
			ID:                    strings.TrimSpace(server.ID),
			Label:                 strings.TrimSpace(server.Label),
			URL:                   redactURLLike(server.URL),
			BearerTokenEnv:        envName,
			BearerTokenConfigured: envName != "" && os.Getenv(envName) != "",
			EnabledTools:          append([]string(nil), server.EnabledTools...),
			DisabledTools:         append([]string(nil), server.DisabledTools...),
			Enabled:               enabled,
		})
	}
	if !cfg.Enabled {
		return status
	}
	status.Readiness = toolsReadinessReady
	if len(cfg.Servers) == 0 {
		status.Readiness = toolsReadinessNotReady
		status.Warnings = append(status.Warnings, "tools.mcp.enabled is true but no servers are configured")
		return status
	}
	if status.EnabledServerCount == 0 {
		status.Readiness = toolsReadinessNotReady
		status.Warnings = append(status.Warnings, "tools.mcp has no enabled servers")
	}
	for idx, server := range cfg.Servers {
		if !server.EnabledOrDefault() {
			continue
		}
		label := doctorMCPToolServerLabel(idx, server)
		if strings.TrimSpace(server.URL) == "" {
			status.Readiness = toolsReadinessNotReady
			status.Warnings = append(status.Warnings, label+" url is required")
		} else if parsed, err := url.Parse(strings.TrimSpace(server.URL)); err != nil || parsed.Scheme == "" || parsed.Host == "" {
			status.Readiness = toolsReadinessNotReady
			status.Warnings = append(status.Warnings, label+" url must be an absolute URL")
		}
		if envName := strings.TrimSpace(server.BearerTokenEnv); envName != "" && os.Getenv(envName) == "" {
			status.Readiness = toolsReadinessNotReady
			status.Warnings = append(status.Warnings, label+" bearer_token_env is set but the environment variable is empty or unset")
		}
	}
	return status
}

func toolsStatusInjectableTools(webSearch toolsStatusWebSearch, mcp toolsStatusMCP) []string {
	var out []string
	if webSearch.Enabled {
		out = append(out, hosted.ToolTypeWebSearch)
	}
	if mcp.Enabled {
		out = append(out, hosted.ToolTypeMCP)
	}
	return out
}

func toolsStatusCodexWebSearchReadiness(result toolsStatusResult) (bool, string) {
	switch {
	case !result.ResponsesServer.Enabled:
		return false, "responses_server.enabled is false"
	case !result.ResponsesServer.CodexCompat.Enabled:
		return false, "responses_server.codex_compat.enabled is false"
	case !toolsStatusContainsWebSearch(result.ResponsesServer.CodexCompat.AutoInjectHostedTools):
		return false, "responses_server.codex_compat.auto_inject_hosted_tools does not include web_search"
	case !toolsStatusContainsWebSearch(result.ResponsesServer.CodexCompat.InjectableTools):
		return false, "web_search is configured for Codex compatibility but is not injectable"
	case result.Tools.WebSearch.Readiness != toolsReadinessReady:
		return false, "tools.web_search is not ready"
	default:
		return true, "web_search is ready for Codex compatibility auto-injection"
	}
}

func toolsStatusContainsWebSearch(tools []string) bool {
	for _, tool := range tools {
		switch strings.ToLower(strings.TrimSpace(tool)) {
		case hosted.ToolTypeWebSearch, "web_search_preview":
			return true
		}
	}
	return false
}

func writeToolsStatusText(w io.Writer, result toolsStatusResult) {
	fmt.Fprintf(w, "tools: ready_for_codex_web_search=%t reason=%s\n", result.ReadyForCodexWebSearch, result.ReadyForCodexWebSearchWhy)
	fmt.Fprintf(w, "responses_server: enabled=%t codex_compat.enabled=%t auto_inject=%s injectable=%s\n",
		result.ResponsesServer.Enabled,
		result.ResponsesServer.CodexCompat.Enabled,
		strings.Join(result.ResponsesServer.CodexCompat.AutoInjectHostedTools, ","),
		strings.Join(result.ResponsesServer.CodexCompat.InjectableTools, ","),
	)
	fmt.Fprintf(w, "tools.web_search: enabled=%t provider=%s base_url_present=%t max_results=%d timeout_ms=%d readiness=%s\n",
		result.Tools.WebSearch.Enabled,
		result.Tools.WebSearch.Provider,
		result.Tools.WebSearch.BaseURLPresent,
		result.Tools.WebSearch.MaxResults,
		result.Tools.WebSearch.TimeoutMS,
		result.Tools.WebSearch.Readiness,
	)
	fmt.Fprintf(w, "tools.mcp: enabled=%t servers=%d enabled_servers=%d readiness=%s\n",
		result.Tools.MCP.Enabled,
		result.Tools.MCP.ServerCount,
		result.Tools.MCP.EnabledServerCount,
		result.Tools.MCP.Readiness,
	)
	for _, warning := range result.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
}
