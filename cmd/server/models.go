package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"

	appconfig "github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

const (
	codexConfigCommand           = "models.codex_config"
	codexConfigModelProvider     = "llm-tracelab"
	codexConfigProviderName      = "llm-tracelab"
	codexConfigAPIKeyEnv         = "LLM_TRACELAB_API_KEY"
	codexConfigWireAPI           = "responses"
	codexConfigRequestMaxRetries = 2
	codexConfigStreamMaxRetries  = 2
	codexConfigStreamIdleTimeout = 120000
)

var modelsProfileAdoptionRequiredGates = []string{"schema_migration", "dry_run_diff", "conflict_report", "rollback_plan", "dsn_gated_tests"}

type modelsCodexConfigOptions struct {
	configPath      string
	codexConfigPath string
	format          string
	stdout          io.Writer
	model           string
}

type modelsCodexConfigResult struct {
	Model       string                    `json:"model"`
	WireAPI     string                    `json:"wire_api"`
	Profile     modelsCodexProfileConfig  `json:"profile"`
	Provider    modelsCodexProviderConfig `json:"provider"`
	Diagnostics modelsCodexDiagnostics    `json:"diagnostics"`
	TOML        string                    `json:"toml"`
	Warnings    []string                  `json:"warnings"`
}

type modelsCodexProfileConfig struct {
	ModelProvider              string `json:"model_provider"`
	Model                      string `json:"model"`
	ModelContextWindow         int    `json:"model_context_window"`
	ModelAutoCompactTokenLimit int    `json:"model_auto_compact_token_limit"`
}

type modelsCodexProviderConfig struct {
	Name                string `json:"name"`
	BaseURL             string `json:"base_url"`
	ResponsesPath       string `json:"responses_path"`
	ServerOrigin        string `json:"server_origin"`
	EnvKey              string `json:"env_key"`
	WireAPI             string `json:"wire_api"`
	RequestMaxRetries   int    `json:"request_max_retries"`
	StreamMaxRetries    int    `json:"stream_max_retries"`
	StreamIdleTimeoutMS int    `json:"stream_idle_timeout_ms"`
	BaseURLSource       string `json:"base_url_source"`
}

type modelsCodexDiagnostics struct {
	MatchedProfile                    modelsCodexMatchedProfile   `json:"matched_profile"`
	RuntimeProfileSource              string                      `json:"runtime_profile_source"`
	ProfilePrecedence                 []string                    `json:"profile_precedence"`
	CatalogProfileRole                string                      `json:"catalog_profile_role"`
	ProviderChannelProfileAdoption    string                      `json:"provider_channel_profile_adoption"`
	ProfileAdoptionReport             modelsProfileAdoptionReport `json:"profile_adoption_report"`
	ProfileConflictStrategy           string                      `json:"profile_conflict_strategy"`
	ProfileAdoptionRequiredGates      []string                    `json:"profile_adoption_required_gates"`
	CapabilitySource                  string                      `json:"capability_source"`
	CompactLimitSource                string                      `json:"compact_limit_source"`
	CompactLimitMarginTokens          int                         `json:"compact_limit_margin_tokens"`
	CompactHistoryItemThreshold       int                         `json:"compact_history_item_threshold"`
	CompactHistoryItemThresholdSource string                      `json:"compact_history_item_threshold_source"`
	ResponsesServerEnabled            bool                        `json:"responses_server_enabled"`
	DatabaseAvailable                 bool                        `json:"database_available"`
	CatalogModelPresent               bool                        `json:"catalog_model_present"`
	ChannelModelPresent               bool                        `json:"channel_model_present"`
	ChannelModelCount                 int                         `json:"channel_model_count"`
	CatalogSource                     string                      `json:"catalog_source"`
	ChannelSource                     string                      `json:"channel_source"`
	DriftWarnings                     []string                    `json:"drift_warnings,omitempty"`
	CodexConfig                       modelsCodexLocalConfig      `json:"codex_config"`
}

type modelsCodexMatchedProfile struct {
	Matched       bool   `json:"matched"`
	Index         int    `json:"index"`
	Kind          string `json:"kind,omitempty"`
	Source        string `json:"source"`
	Name          string `json:"name,omitempty"`
	Pattern       string `json:"pattern,omitempty"`
	UpstreamModel string `json:"upstream_model,omitempty"`
}

type modelsProfileAdoptionReport struct {
	Mode                  string                           `json:"mode"`
	DryRun                bool                             `json:"dry_run"`
	Mutates               bool                             `json:"mutates"`
	Status                string                           `json:"status"`
	RuntimeProfileSource  string                           `json:"runtime_profile_source"`
	CandidateSource       string                           `json:"candidate_source"`
	ExplicitConfigPresent bool                             `json:"explicit_config_present"`
	AdoptionReady         bool                             `json:"adoption_ready"`
	CandidateCount        int                              `json:"candidate_count"`
	ProposedChangeCount   int                              `json:"proposed_change_count"`
	ConflictCount         int                              `json:"conflict_count"`
	BlockingGateCount     int                              `json:"blocking_gate_count"`
	BlockedReasons        []string                         `json:"blocked_reasons,omitempty"`
	RequiredGates         []modelsProfileAdoptionGate      `json:"required_gates"`
	Fields                []modelsProfileAdoptionFieldDiff `json:"fields,omitempty"`
	Candidates            []modelsProfileAdoptionCandidate `json:"candidates,omitempty"`
	Conflicts             []modelsProfileAdoptionConflict  `json:"conflicts,omitempty"`
}

type modelsProfileAdoptionGate struct {
	Gate     string `json:"gate"`
	Status   string `json:"status"`
	Blocking bool   `json:"blocking"`
	Reason   string `json:"reason,omitempty"`
}

type modelsProfileAdoptionFieldDiff struct {
	Field           string `json:"field"`
	RuntimeValue    int    `json:"runtime_value"`
	CandidateValue  int    `json:"candidate_value"`
	RuntimeSource   string `json:"runtime_source"`
	CandidateSource string `json:"candidate_source"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
}

type modelsProfileAdoptionCandidate struct {
	ChannelID               string `json:"channel_id"`
	Model                   string `json:"model"`
	Source                  string `json:"source"`
	Enabled                 bool   `json:"enabled"`
	ContextWindowTokens     int    `json:"context_window_tokens,omitempty"`
	SupportsResponses       string `json:"supports_responses"`
	SupportsChatCompletions string `json:"supports_chat_completions"`
	SupportsEmbeddings      string `json:"supports_embeddings"`
	Eligible                bool   `json:"eligible"`
	BlockedReason           string `json:"blocked_reason,omitempty"`
}

type modelsProfileAdoptionConflict struct {
	Field           string `json:"field"`
	RuntimeValue    int    `json:"runtime_value"`
	CandidateValue  int    `json:"candidate_value"`
	RuntimeSource   string `json:"runtime_source"`
	CandidateSource string `json:"candidate_source"`
	Strategy        string `json:"strategy"`
}

type modelsCodexLocalConfig struct {
	Path            string                        `json:"path,omitempty"`
	Status          string                        `json:"status"`
	Present         bool                          `json:"present"`
	Readable        bool                          `json:"readable"`
	Parsed          bool                          `json:"parsed"`
	ProfileName     string                        `json:"profile_name,omitempty"`
	ProviderName    string                        `json:"provider_name,omitempty"`
	ProfilePresent  bool                          `json:"profile_present"`
	ProviderPresent bool                          `json:"provider_present"`
	Fields          []modelsCodexLocalFieldStatus `json:"fields,omitempty"`
	DriftWarnings   []string                      `json:"drift_warnings"`
}

type modelsCodexLocalFieldStatus struct {
	Field    string `json:"field"`
	Present  bool   `json:"present"`
	Matched  bool   `json:"matched"`
	Expected string `json:"expected"`
	Actual   string `json:"actual,omitempty"`
}

func newModelsCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "models",
		Short:         "Inspect model-related local configuration",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	cmd.AddCommand(newModelsCodexConfigCommand(runtime))
	return cmd
}

func newModelsCodexConfigCommand(runtime *cliRuntime) *cobra.Command {
	var codexConfigPath string
	cmd := &cobra.Command{
		Use:   "codex-config <model>",
		Short: "Generate a Codex profile TOML suggestion from local Responses model profiles",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCode(func() int {
				return runModelsCodexConfigWithOptions(modelsCodexConfigOptions{
					configPath:      runtime.configPath(),
					codexConfigPath: codexConfigPath,
					format:          runtime.outputFormat(),
					stdout:          cmd.OutOrStdout(),
					model:           args[0],
				})
			})
		},
	}
	cmd.Flags().StringVar(&codexConfigPath, "codex-config", "", "Path to a local Codex TOML config file to diagnose for drift")
	return cmd
}

func runModelsCodexConfigWithOptions(opts modelsCodexConfigOptions) int {
	cfg, err := appconfig.Load(opts.configPath)
	if err != nil {
		slog.Error("Failed to load config", "path", opts.configPath, "error", err)
		return 1
	}
	result := buildModelsCodexConfigResult(cfg, opts.model, opts.codexConfigPath)
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, codexConfigCommand, result, func(w io.Writer) error {
		writeModelsCodexConfigText(w, result)
		return nil
	}); err != nil {
		slog.Error("Write models codex-config result failed", "error", err)
		return 1
	}
	return 0
}

func buildModelsCodexConfigResult(cfg *appconfig.Config, model string, codexConfigPath string) modelsCodexConfigResult {
	if cfg == nil {
		cfg = &appconfig.Config{}
	}
	model = strings.TrimSpace(model)
	match := cfg.MatchResponsesModelProfile(model)
	profile := match.Profile
	warnings := make([]string, 0, 4)
	if !cfg.ResponsesServerEnabled() {
		warnings = append(warnings, "responses_server.enabled is false; this is a configuration suggestion only until server-mode is enabled")
	}
	if !match.Matched {
		warnings = append(warnings, fmt.Sprintf("no responses_server.model_profiles entry matched model %q; using zero model limits", model))
	}
	contextWindow := profile.ContextWindowTokens
	autoCompactLimit := 0
	compactLimitSource := "none"
	if contextWindow > 0 {
		autoCompactLimit = contextWindow * 8 / 10
		if match.Matched {
			compactLimitSource = match.Source + ".context_window_tokens_80_percent"
		} else {
			compactLimitSource = "context_window_80_percent"
		}
	} else if match.Matched {
		warnings = append(warnings, fmt.Sprintf("matched profile for model %q has no context_window_tokens; Codex token limits are emitted as 0", model))
	}
	provider, providerWarnings := buildModelsCodexProviderConfig(cfg)
	warnings = append(warnings, providerWarnings...)
	historyThreshold, historyThresholdSource := codexCompactHistoryItemThreshold(cfg, match)
	catalogDiagnostics := buildModelsCatalogDriftDiagnostics(cfg, model, match.Matched)
	warnings = append(warnings, catalogDiagnostics.DriftWarnings...)
	codexConfigDiagnostics := modelsCodexLocalConfig{Status: "not_configured", DriftWarnings: []string{}}
	diagnostics := modelsCodexDiagnostics{
		MatchedProfile: modelsCodexMatchedProfile{
			Matched:       match.Matched,
			Index:         match.Index,
			Kind:          match.Kind,
			Source:        match.Source,
			Name:          profile.Name,
			Pattern:       profile.Pattern,
			UpstreamModel: profile.UpstreamModel,
		},
		RuntimeProfileSource:              "responses_server.model_profiles",
		ProfilePrecedence:                 []string{"responses_server.model_profiles", "zero_limits_when_unmatched"},
		CatalogProfileRole:                "diagnostic_only",
		ProviderChannelProfileAdoption:    "observe_only",
		ProfileAdoptionReport:             buildModelsProfileAdoptionReport(match, catalogDiagnostics),
		ProfileConflictStrategy:           "responses_server.model_profiles_wins",
		ProfileAdoptionRequiredGates:      modelsProfileAdoptionRequiredGates,
		CapabilitySource:                  "provider_upstream_capabilities",
		CompactLimitSource:                compactLimitSource,
		CompactLimitMarginTokens:          contextWindow - autoCompactLimit,
		CompactHistoryItemThreshold:       historyThreshold,
		CompactHistoryItemThresholdSource: historyThresholdSource,
		ResponsesServerEnabled:            cfg.ResponsesServerEnabled(),
		DatabaseAvailable:                 catalogDiagnostics.DatabaseAvailable,
		CatalogModelPresent:               catalogDiagnostics.CatalogModelPresent,
		ChannelModelPresent:               catalogDiagnostics.ChannelModelPresent,
		ChannelModelCount:                 catalogDiagnostics.ChannelModelCount,
		CatalogSource:                     catalogDiagnostics.CatalogSource,
		ChannelSource:                     catalogDiagnostics.ChannelSource,
		DriftWarnings:                     catalogDiagnostics.DriftWarnings,
		CodexConfig:                       codexConfigDiagnostics,
	}
	result := modelsCodexConfigResult{
		Model:   model,
		WireAPI: codexConfigWireAPI,
		Profile: modelsCodexProfileConfig{
			ModelProvider:              codexConfigModelProvider,
			Model:                      model,
			ModelContextWindow:         contextWindow,
			ModelAutoCompactTokenLimit: autoCompactLimit,
		},
		Provider:    provider,
		Diagnostics: diagnostics,
		Warnings:    warnings,
	}
	result.TOML = modelsCodexConfigTOML(result)
	result.Diagnostics.CodexConfig = diagnoseModelsCodexLocalConfig(codexConfigPath, result)
	warnings = append(warnings, result.Diagnostics.CodexConfig.DriftWarnings...)
	result.Warnings = warnings
	return result
}

type modelsCatalogDriftDiagnostics struct {
	DatabaseAvailable   bool
	CatalogModelPresent bool
	ChannelModelPresent bool
	ChannelModelCount   int
	CatalogSource       string
	ChannelSource       string
	DriftWarnings       []string
	ChannelModels       []store.ChannelModelRecord
}

func buildModelsCatalogDriftDiagnostics(cfg *appconfig.Config, model string, profileMatched bool) modelsCatalogDriftDiagnostics {
	diagnostics := modelsCatalogDriftDiagnostics{
		CatalogSource: "unavailable",
		ChannelSource: "unavailable",
	}
	if cfg == nil || cfg.DatabaseDriver() != "sqlite" {
		return diagnostics
	}
	dbPath := cfg.DatabasePath()
	if strings.TrimSpace(dbPath) == "" || dbPath == ":memory:" {
		return diagnostics
	}
	if _, err := os.Stat(dbPath); err != nil {
		return diagnostics
	}
	outputDir := cfg.TraceOutputDir()
	if strings.TrimSpace(outputDir) == "" {
		outputDir = "."
	}

	st, err := store.NewWithDatabaseOptions(
		outputDir,
		cfg.DatabaseDriver(),
		cfg.DatabaseDSN(),
		cfg.DatabaseMaxOpenConns(),
		cfg.DatabaseMaxIdleConns(),
		store.DatabaseOptions{AutoMigrate: false},
	)
	if err != nil {
		return diagnostics
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Debug("Close application store after model diagnostics failed", "error", err)
		}
	}()

	model = strings.ToLower(strings.TrimSpace(model))
	channelModels, err := st.ListChannelModels("", false)
	if err != nil {
		return diagnostics
	}
	diagnostics.DatabaseAvailable = true
	diagnostics.ChannelSource = "missing"
	for _, channelModel := range channelModels {
		if strings.ToLower(strings.TrimSpace(channelModel.Model)) != model {
			continue
		}
		diagnostics.ChannelModels = append(diagnostics.ChannelModels, channelModel)
		diagnostics.ChannelModelCount++
		diagnostics.ChannelModelPresent = true
		diagnostics.ChannelSource = "channel_models"
	}

	if _, err := st.GetModelCatalog(model); err == nil {
		diagnostics.CatalogModelPresent = true
		diagnostics.CatalogSource = "model_catalog"
	} else if errors.Is(err, sql.ErrNoRows) {
		diagnostics.CatalogSource = "missing"
	} else {
		return modelsCatalogDriftDiagnostics{
			CatalogSource: "unavailable",
			ChannelSource: "unavailable",
		}
	}

	diagnostics.DriftWarnings = modelsCatalogDriftWarnings(model, profileMatched, diagnostics)
	return diagnostics
}

func modelsCatalogDriftWarnings(model string, profileMatched bool, diagnostics modelsCatalogDriftDiagnostics) []string {
	if !diagnostics.DatabaseAvailable {
		return nil
	}
	warnings := make([]string, 0, 2)
	if profileMatched && !diagnostics.CatalogModelPresent {
		warnings = append(warnings, fmt.Sprintf("matched responses_server.model_profiles for model %q, but model_catalog has no entry for it", model))
	}
	if profileMatched && !diagnostics.ChannelModelPresent {
		warnings = append(warnings, fmt.Sprintf("matched responses_server.model_profiles for model %q, but channel_models has no entry for it", model))
	}
	if diagnostics.ChannelModelPresent && !diagnostics.CatalogModelPresent {
		warnings = append(warnings, fmt.Sprintf("channel_models contains model %q, but model_catalog has no entry for it", model))
	}
	if diagnostics.CatalogModelPresent && !diagnostics.ChannelModelPresent {
		warnings = append(warnings, fmt.Sprintf("model_catalog contains model %q, but channel_models has no entry for it", model))
	}
	return warnings
}

func buildModelsProfileAdoptionReport(match appconfig.ResponsesModelProfileMatch, diagnostics modelsCatalogDriftDiagnostics) modelsProfileAdoptionReport {
	report := modelsProfileAdoptionReport{
		Mode:                  "observe_only",
		DryRun:                true,
		Mutates:               false,
		Status:                "unavailable",
		RuntimeProfileSource:  "responses_server.model_profiles",
		CandidateSource:       diagnostics.ChannelSource,
		ExplicitConfigPresent: match.Matched,
		BlockedReasons:        []string{},
	}
	report.RequiredGates, report.BlockingGateCount = buildModelsProfileAdoptionRequiredGateStatuses()
	if report.BlockingGateCount == 0 {
		report.AdoptionReady = true
	}
	if !diagnostics.DatabaseAvailable {
		report.BlockedReasons = append(report.BlockedReasons, "application_database_unavailable")
		return report
	}
	report.Status = "no_candidate"
	if len(diagnostics.ChannelModels) == 0 {
		report.BlockedReasons = append(report.BlockedReasons, "channel_model_missing")
		return report
	}

	for _, channelModel := range diagnostics.ChannelModels {
		candidate := modelsProfileAdoptionCandidate{
			ChannelID:               channelModel.ChannelID,
			Model:                   channelModel.Model,
			Source:                  channelModel.Source,
			Enabled:                 channelModel.Enabled,
			SupportsResponses:       triStateCapability(channelModel.SupportsResponses),
			SupportsChatCompletions: triStateCapability(channelModel.SupportsChatCompletions),
			SupportsEmbeddings:      triStateCapability(channelModel.SupportsEmbeddings),
		}
		if channelModel.ContextWindow != nil {
			candidate.ContextWindowTokens = *channelModel.ContextWindow
		}
		candidate.Eligible, candidate.BlockedReason = modelsProfileAdoptionCandidateEligibility(channelModel)
		report.Candidates = append(report.Candidates, candidate)
		if candidate.Eligible {
			report.CandidateCount++
		}
	}
	if report.CandidateCount == 0 {
		report.Status = "blocked"
		report.BlockedReasons = append(report.BlockedReasons, "no_eligible_channel_profile_candidate")
		return report
	}

	contextWindow, source, conflict := selectModelsProfileAdoptionContextWindowCandidate(report.Candidates)
	if conflict {
		report.Status = "conflict"
		report.ConflictCount++
		report.BlockedReasons = append(report.BlockedReasons, "conflicting_channel_context_window_candidates")
		return report
	}
	if contextWindow <= 0 {
		report.Status = "no_candidate"
		report.BlockedReasons = append(report.BlockedReasons, "channel_context_window_missing")
		return report
	}

	runtimeValue := 0
	if match.Matched {
		runtimeValue = match.Profile.ContextWindowTokens
	}
	field := modelsProfileAdoptionFieldDiff{
		Field:           "context_window_tokens",
		RuntimeValue:    runtimeValue,
		CandidateValue:  contextWindow,
		RuntimeSource:   match.Source,
		CandidateSource: source,
	}
	if match.Matched {
		if runtimeValue == contextWindow {
			field.Status = "no_change"
			report.Status = "no_change"
		} else {
			field.Status = "blocked_explicit_config"
			field.Reason = "explicit responses_server.model_profiles entry wins in observe_only adoption"
			report.Status = "blocked"
			report.ConflictCount++
			report.BlockedReasons = append(report.BlockedReasons, "explicit_runtime_profile_present")
			report.Conflicts = append(report.Conflicts, modelsProfileAdoptionConflict{
				Field:           field.Field,
				RuntimeValue:    runtimeValue,
				CandidateValue:  contextWindow,
				RuntimeSource:   field.RuntimeSource,
				CandidateSource: field.CandidateSource,
				Strategy:        "responses_server.model_profiles_wins",
			})
		}
	} else {
		field.Status = "would_adopt_after_gates"
		field.Reason = "candidate is reported for future adoption only; runtime profile remains unchanged"
		report.Status = "would_change"
		report.ProposedChangeCount = 1
	}
	report.Fields = append(report.Fields, field)
	return report
}

func buildModelsProfileAdoptionRequiredGateStatuses() ([]modelsProfileAdoptionGate, int) {
	gates := []modelsProfileAdoptionGate{
		{
			Gate:     "schema_migration",
			Status:   "blocking_not_implemented",
			Blocking: true,
			Reason:   "catalog/channel profile fields are not covered by a runtime adoption schema migration",
		},
		{
			Gate:     "dry_run_diff",
			Status:   "implemented_observe_only",
			Blocking: false,
			Reason:   "profile_adoption_report reports candidate diffs without mutating runtime config",
		},
		{
			Gate:     "conflict_report",
			Status:   "implemented_observe_only",
			Blocking: false,
			Reason:   "profile_adoption_report reports explicit runtime profile conflicts and capability false blocks",
		},
		{
			Gate:     "rollback_plan",
			Status:   "blocking_not_implemented",
			Blocking: true,
			Reason:   "there is no rollback contract for catalog/channel profile adoption into runtime profiles",
		},
		{
			Gate:     "dsn_gated_tests",
			Status:   "blocking_not_implemented",
			Blocking: true,
			Reason:   "real database adoption tests are not yet covered by an opt-in DSN-gated suite",
		},
	}
	blockingCount := 0
	for _, gate := range gates {
		if gate.Blocking {
			blockingCount++
		}
	}
	return gates, blockingCount
}

func modelsProfileAdoptionCandidateEligibility(channelModel store.ChannelModelRecord) (bool, string) {
	if !channelModel.Enabled {
		return false, "channel_model_disabled"
	}
	if channelModel.SupportsChatCompletions != nil && *channelModel.SupportsChatCompletions == 0 {
		return false, "capability_false_chat_completions"
	}
	if channelModel.ContextWindow == nil || *channelModel.ContextWindow <= 0 {
		return false, "context_window_missing"
	}
	return true, ""
}

func selectModelsProfileAdoptionContextWindowCandidate(candidates []modelsProfileAdoptionCandidate) (int, string, bool) {
	value := 0
	source := ""
	for _, candidate := range candidates {
		if !candidate.Eligible || candidate.ContextWindowTokens <= 0 {
			continue
		}
		candidateSource := "channel_models." + candidate.ChannelID + ".context_window"
		if value == 0 {
			value = candidate.ContextWindowTokens
			source = candidateSource
			continue
		}
		if value != candidate.ContextWindowTokens {
			return 0, "", true
		}
	}
	return value, source, false
}

func triStateCapability(value *int) string {
	if value == nil {
		return "unknown"
	}
	if *value == 0 {
		return "false"
	}
	return "true"
}

func diagnoseModelsCodexLocalConfig(path string, suggestion modelsCodexConfigResult) modelsCodexLocalConfig {
	path = strings.TrimSpace(path)
	diagnostics := modelsCodexLocalConfig{
		Path:          path,
		Status:        "not_configured",
		ProfileName:   suggestion.Model,
		ProviderName:  suggestion.Profile.ModelProvider,
		DriftWarnings: []string{},
	}
	if path == "" {
		return diagnostics
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			diagnostics.Status = "missing"
			diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, "codex config file does not exist")
			return diagnostics
		}
		diagnostics.Status = "unreadable"
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, "codex config file is not readable")
		return diagnostics
	}
	diagnostics.Present = true
	if info.IsDir() {
		diagnostics.Status = "unreadable"
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, "codex config path is a directory")
		return diagnostics
	}

	body, err := os.ReadFile(path)
	if err != nil {
		diagnostics.Status = "unreadable"
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, "codex config file is not readable")
		return diagnostics
	}
	diagnostics.Readable = true

	var document map[string]any
	if err := toml.Unmarshal(body, &document); err != nil {
		diagnostics.Status = "parse_error"
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, "codex config file is invalid TOML")
		return diagnostics
	}
	diagnostics.Parsed = true

	profiles := tomlChildMap(document, "profiles")
	profile := tomlChildMap(profiles, suggestion.Model)
	diagnostics.ProfilePresent = profile != nil
	if !diagnostics.ProfilePresent {
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, fmt.Sprintf("codex config profile %q is missing", suggestion.Model))
	}

	providers := tomlChildMap(document, "model_providers")
	provider := tomlChildMap(providers, suggestion.Profile.ModelProvider)
	diagnostics.ProviderPresent = provider != nil
	if !diagnostics.ProviderPresent {
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, fmt.Sprintf("codex config provider %q is missing", suggestion.Profile.ModelProvider))
	}

	diagnostics.Fields = append(diagnostics.Fields,
		compareModelsCodexStringField(profile, "profile.model_provider", "model_provider", suggestion.Profile.ModelProvider, false),
		compareModelsCodexStringField(profile, "profile.model", "model", suggestion.Profile.Model, false),
		compareModelsCodexIntField(profile, "profile.model_context_window", "model_context_window", suggestion.Profile.ModelContextWindow),
		compareModelsCodexIntField(profile, "profile.model_auto_compact_token_limit", "model_auto_compact_token_limit", suggestion.Profile.ModelAutoCompactTokenLimit),
		compareModelsCodexStringField(provider, "provider.base_url", "base_url", suggestion.Provider.BaseURL, true),
		compareModelsCodexStringField(provider, "provider.wire_api", "wire_api", suggestion.Provider.WireAPI, false),
		compareModelsCodexStringField(provider, "provider.env_key", "env_key", suggestion.Provider.EnvKey, false),
	)
	for _, field := range diagnostics.Fields {
		if field.Matched {
			continue
		}
		if !field.Present {
			diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, fmt.Sprintf("codex config field %s is missing; expected %s", field.Field, field.Expected))
			continue
		}
		diagnostics.DriftWarnings = append(diagnostics.DriftWarnings, fmt.Sprintf("codex config field %s is %s; expected %s", field.Field, field.Actual, field.Expected))
	}

	if len(diagnostics.DriftWarnings) > 0 {
		diagnostics.Status = "drift"
		return diagnostics
	}
	diagnostics.Status = "ok"
	return diagnostics
}

func tomlChildMap(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return nil
	}
	child, ok := parent[key]
	if !ok {
		return nil
	}
	childMap, ok := child.(map[string]any)
	if !ok {
		return nil
	}
	return childMap
}

func compareModelsCodexStringField(parent map[string]any, field string, key string, expected string, redactURL bool) modelsCodexLocalFieldStatus {
	status := modelsCodexLocalFieldStatus{
		Field:    field,
		Expected: expected,
	}
	if redactURL {
		status.Expected = redactURLLike(expected)
	}
	if parent == nil {
		return status
	}
	raw, ok := parent[key]
	if !ok {
		return status
	}
	status.Present = true
	actual, ok := raw.(string)
	if !ok {
		status.Actual = fmt.Sprintf("<%T>", raw)
		return status
	}
	status.Matched = actual == expected
	if redactURL {
		status.Actual = redactURLLike(actual)
	} else {
		status.Actual = actual
	}
	return status
}

func compareModelsCodexIntField(parent map[string]any, field string, key string, expected int) modelsCodexLocalFieldStatus {
	status := modelsCodexLocalFieldStatus{
		Field:    field,
		Expected: strconv.Itoa(expected),
	}
	if parent == nil {
		return status
	}
	raw, ok := parent[key]
	if !ok {
		return status
	}
	status.Present = true
	actual, ok := tomlInt(raw)
	if !ok {
		status.Actual = fmt.Sprintf("<%T>", raw)
		return status
	}
	status.Actual = strconv.Itoa(actual)
	status.Matched = actual == expected
	return status
}

func tomlInt(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case int32:
		return int(value), true
	case uint:
		if value > uint(^uint(0)>>1) {
			return 0, false
		}
		return int(value), true
	case uint64:
		if value > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(value), true
	case uint32:
		return int(value), true
	default:
		return 0, false
	}
}

func buildModelsCodexProviderConfig(cfg *appconfig.Config) (modelsCodexProviderConfig, []string) {
	warnings := []string{}
	origin, originWarnings := codexServerOrigin(cfg.Server.Port)
	warnings = append(warnings, originWarnings...)
	responsesPath := normalizeCodexResponsesPath(cfg.ResponsesServerPath())
	baseURLPath := strings.TrimSuffix(responsesPath, "/responses")
	baseURLSource := "responses_server.path without trailing /responses"
	if responsesPath == "/responses" {
		baseURLPath = ""
	}
	if !strings.HasSuffix(responsesPath, "/responses") {
		baseURLPath = strings.TrimRight(responsesPath, "/")
		baseURLSource = "responses_server.path"
		warnings = append(warnings, fmt.Sprintf("responses_server.path %q does not end with /responses; Codex wire_api=responses normally appends /responses to provider base_url", responsesPath))
	}
	baseURL := origin
	if baseURLPath != "" && baseURLPath != "/" {
		baseURL += baseURLPath
	}
	return modelsCodexProviderConfig{
		Name:                codexConfigProviderName,
		BaseURL:             baseURL,
		ResponsesPath:       responsesPath,
		ServerOrigin:        origin,
		EnvKey:              codexConfigAPIKeyEnv,
		WireAPI:             codexConfigWireAPI,
		RequestMaxRetries:   codexConfigRequestMaxRetries,
		StreamMaxRetries:    codexConfigStreamMaxRetries,
		StreamIdleTimeoutMS: codexConfigStreamIdleTimeout,
		BaseURLSource:       baseURLSource,
	}, warnings
}

func codexServerOrigin(port string) (string, []string) {
	port = strings.TrimSpace(strings.TrimPrefix(port, ":"))
	if port == "" {
		return "http://127.0.0.1", []string{"server.port is empty; provider base_url omits a port"}
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "http://127.0.0.1:" + port, []string{fmt.Sprintf("server.port %q is not a plain TCP port; provider base_url uses it verbatim", port)}
	}
	return "http://127.0.0.1:" + port, nil
}

func normalizeCodexResponsesPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/v1/responses"
	}
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func codexCompactHistoryItemThreshold(cfg *appconfig.Config, match appconfig.ResponsesModelProfileMatch) (int, string) {
	if match.Matched && match.Profile.CompactHistoryItemThreshold > 0 {
		return match.Profile.CompactHistoryItemThreshold, match.Source + ".compact_history_item_threshold"
	}
	if cfg.ResponsesCompactHistoryItemThreshold() > 0 {
		return cfg.ResponsesCompactHistoryItemThreshold(), "responses_server.compact_history_item_threshold"
	}
	return 0, "none"
}

func writeModelsCodexConfigText(w io.Writer, result modelsCodexConfigResult) {
	fmt.Fprintf(w, "# llm-tracelab Codex config suggestion\n")
	fmt.Fprintf(w, "# model: %s\n", result.Model)
	if len(result.Warnings) > 0 {
		fmt.Fprintln(w, "# warnings:")
		for _, warning := range result.Warnings {
			fmt.Fprintf(w, "# - %s\n", warning)
		}
	}
	fmt.Fprintf(w, "# diagnostics: database_available=%t catalog_model_present=%t channel_model_present=%t channel_model_count=%d catalog_source=%s channel_source=%s\n",
		result.Diagnostics.DatabaseAvailable,
		result.Diagnostics.CatalogModelPresent,
		result.Diagnostics.ChannelModelPresent,
		result.Diagnostics.ChannelModelCount,
		result.Diagnostics.CatalogSource,
		result.Diagnostics.ChannelSource,
	)
	fmt.Fprintf(w, "# profile_sources: runtime_profile_source=%s catalog_profile_role=%s capability_source=%s precedence=%s\n",
		result.Diagnostics.RuntimeProfileSource,
		result.Diagnostics.CatalogProfileRole,
		result.Diagnostics.CapabilitySource,
		strings.Join(result.Diagnostics.ProfilePrecedence, ","),
	)
	fmt.Fprintf(w, "# profile_adoption: provider_channel_profile_adoption=%s conflict_strategy=%s required_gates=%s\n",
		result.Diagnostics.ProviderChannelProfileAdoption,
		result.Diagnostics.ProfileConflictStrategy,
		strings.Join(result.Diagnostics.ProfileAdoptionRequiredGates, ","),
	)
	fmt.Fprintf(w, "# profile_adoption_report: mode=%s dry_run=%t mutates=%t status=%s proposed_changes=%d conflicts=%d blocked_reasons=%s\n",
		result.Diagnostics.ProfileAdoptionReport.Mode,
		result.Diagnostics.ProfileAdoptionReport.DryRun,
		result.Diagnostics.ProfileAdoptionReport.Mutates,
		result.Diagnostics.ProfileAdoptionReport.Status,
		result.Diagnostics.ProfileAdoptionReport.ProposedChangeCount,
		result.Diagnostics.ProfileAdoptionReport.ConflictCount,
		strings.Join(result.Diagnostics.ProfileAdoptionReport.BlockedReasons, ","),
	)
	fmt.Fprintf(w, "# profile_adoption_gates: adoption_ready=%t blocking_gate_count=%d required_gate_statuses=%s\n",
		result.Diagnostics.ProfileAdoptionReport.AdoptionReady,
		result.Diagnostics.ProfileAdoptionReport.BlockingGateCount,
		formatModelsProfileAdoptionGateStatuses(result.Diagnostics.ProfileAdoptionReport.RequiredGates),
	)
	fmt.Fprintf(w, "# codex_config: status=%s present=%t readable=%t parsed=%t profile_present=%t provider_present=%t",
		result.Diagnostics.CodexConfig.Status,
		result.Diagnostics.CodexConfig.Present,
		result.Diagnostics.CodexConfig.Readable,
		result.Diagnostics.CodexConfig.Parsed,
		result.Diagnostics.CodexConfig.ProfilePresent,
		result.Diagnostics.CodexConfig.ProviderPresent,
	)
	if result.Diagnostics.CodexConfig.Path != "" {
		fmt.Fprintf(w, " path=%s", result.Diagnostics.CodexConfig.Path)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)
	fmt.Fprint(w, result.TOML)
}

func formatModelsProfileAdoptionGateStatuses(gates []modelsProfileAdoptionGate) string {
	if len(gates) == 0 {
		return ""
	}
	parts := make([]string, 0, len(gates))
	for _, gate := range gates {
		status := gate.Gate + ":" + gate.Status
		if gate.Blocking {
			status += ":blocking"
		}
		parts = append(parts, status)
	}
	return strings.Join(parts, ",")
}

func modelsCodexConfigTOML(result modelsCodexConfigResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "model_provider = %s\n", tomlString(result.Profile.ModelProvider))
	fmt.Fprintf(&b, "model = %s\n", tomlString(result.Profile.Model))
	fmt.Fprintf(&b, "model_context_window = %d\n", result.Profile.ModelContextWindow)
	fmt.Fprintf(&b, "model_auto_compact_token_limit = %d\n\n", result.Profile.ModelAutoCompactTokenLimit)
	fmt.Fprintf(&b, "[model_providers.%s]\n", tomlBareKey(result.Profile.ModelProvider))
	fmt.Fprintf(&b, "name = %s\n", tomlString(result.Provider.Name))
	fmt.Fprintf(&b, "base_url = %s\n", tomlString(result.Provider.BaseURL))
	fmt.Fprintf(&b, "env_key = %s\n", tomlString(result.Provider.EnvKey))
	fmt.Fprintf(&b, "wire_api = %s\n", tomlString(result.Provider.WireAPI))
	fmt.Fprintf(&b, "request_max_retries = %d\n", result.Provider.RequestMaxRetries)
	fmt.Fprintf(&b, "stream_max_retries = %d\n", result.Provider.StreamMaxRetries)
	fmt.Fprintf(&b, "stream_idle_timeout_ms = %d\n", result.Provider.StreamIdleTimeoutMS)
	return b.String()
}

func tomlString(value string) string {
	return strconv.Quote(value)
}

func tomlBareKey(value string) string {
	if value == "" {
		return `""`
	}
	for _, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return strconv.Quote(value)
	}
	return value
}
