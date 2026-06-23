package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	responsesaudit "github.com/kingfs/llm-tracelab/internal/responses/audit"
	"github.com/spf13/cobra"
)

type auditQueryOptions struct {
	configPath       string
	format           string
	stdout           io.Writer
	responseID       string
	requestAuditID   string
	clientRequestID  string
	conversationID   string
	status           string
	operation        string
	includeEvents    bool
	includeExchanges bool
	includeTools     bool
	list             bool
	limit            int
}

type auditToolCallAuditsOptions struct {
	configPath      string
	format          string
	stdout          io.Writer
	responseID      string
	requestAuditID  string
	conversationID  string
	callID          string
	toolName        string
	status          string
	includePayloads bool
	limit           int
}

type auditQueryResult struct {
	Query             auditQuerySelector         `json:"query"`
	Found             bool                       `json:"found"`
	Count             int                        `json:"count,omitempty"`
	RequestAudit      *auditRequestAuditView     `json:"request_audit,omitempty"`
	RequestAudits     []auditRequestAuditSummary `json:"request_audits,omitempty"`
	Diagnostics       *auditDiagnosticsView      `json:"diagnostics,omitempty"`
	Events            []auditExecutionEventView  `json:"events"`
	UpstreamExchanges []auditUpstreamExchange    `json:"upstream_exchanges"`
	ToolCalls         []auditToolCallView        `json:"tool_calls"`
}

type auditToolCallAuditsResult struct {
	Query          auditToolCallAuditsSelector `json:"query"`
	Count          int                         `json:"count"`
	ToolCallAudits []auditToolCallAuditView    `json:"tool_call_audits"`
}

type auditQuerySelector struct {
	ResponseID       string `json:"response_id,omitempty"`
	RequestAuditID   string `json:"request_audit_id,omitempty"`
	ClientRequestID  string `json:"client_request_id,omitempty"`
	ConversationID   string `json:"conversation_id,omitempty"`
	Status           string `json:"status,omitempty"`
	Operation        string `json:"operation,omitempty"`
	IncludeEvents    bool   `json:"include_events"`
	IncludeExchanges bool   `json:"include_exchanges"`
	IncludeTools     bool   `json:"include_tools"`
	List             bool   `json:"list"`
	Limit            int    `json:"limit,omitempty"`
}

type auditToolCallAuditsSelector struct {
	ResponseID      string `json:"response_id,omitempty"`
	RequestAuditID  string `json:"request_audit_id,omitempty"`
	ConversationID  string `json:"conversation_id,omitempty"`
	CallID          string `json:"call_id,omitempty"`
	ToolName        string `json:"tool_name,omitempty"`
	Status          string `json:"status,omitempty"`
	IncludePayloads bool   `json:"include_payloads"`
	Limit           int    `json:"limit,omitempty"`
}

type auditRequestAuditView struct {
	ID              string         `json:"id"`
	ResponseID      string         `json:"response_id,omitempty"`
	ConversationID  string         `json:"conversation_id,omitempty"`
	Method          string         `json:"method"`
	Path            string         `json:"path"`
	Operation       string         `json:"operation,omitempty"`
	ClientRequestID string         `json:"client_request_id,omitempty"`
	HeaderJSON      map[string]any `json:"header_json,omitempty"`
	BodyPreview     string         `json:"body_preview,omitempty"`
	BodySHA256      string         `json:"body_sha256,omitempty"`
	RedactionJSON   map[string]any `json:"redaction_json,omitempty"`
	Status          string         `json:"status,omitempty"`
	ErrorText       string         `json:"error_text,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

type auditRequestAuditSummary struct {
	ID              string    `json:"id"`
	ResponseID      string    `json:"response_id,omitempty"`
	ConversationID  string    `json:"conversation_id,omitempty"`
	ClientRequestID string    `json:"client_request_id,omitempty"`
	Status          string    `json:"status,omitempty"`
	Operation       string    `json:"operation,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type auditPendingToolCallDiagnostic struct {
	CallID       string   `json:"call_id,omitempty"`
	ToolName     string   `json:"tool_name,omitempty"`
	Executor     string   `json:"executor,omitempty"`
	LatestStatus string   `json:"latest_status,omitempty"`
	StatusesSeen []string `json:"statuses_seen,omitempty"`
}

type auditCompactSummaryDiagnostic struct {
	EventCount       int       `json:"event_count"`
	AutoTriggered    bool      `json:"auto_triggered"`
	LatestEventID    string    `json:"latest_event_id,omitempty"`
	LatestStatus     string    `json:"latest_status,omitempty"`
	LatestOccurredAt time.Time `json:"latest_occurred_at,omitempty"`
}

type auditDiagnosticsView struct {
	EventCount            int                              `json:"event_count"`
	UpstreamExchangeCount int                              `json:"upstream_exchange_count"`
	ToolCallCount         int                              `json:"tool_call_count"`
	LatestStatus          string                           `json:"latest_status,omitempty"`
	HasCancelled          bool                             `json:"has_cancelled"`
	HasFailed             bool                             `json:"has_failed"`
	HasStreamEvents       bool                             `json:"has_stream_events"`
	HasCompactEvents      bool                             `json:"has_compact_events"`
	PendingToolCallCount  int                              `json:"pending_tool_call_count"`
	PendingToolCalls      []auditPendingToolCallDiagnostic `json:"pending_tool_calls,omitempty"`
	CompactCandidate      bool                             `json:"compact_candidate"`
	CompactSummary        *auditCompactSummaryDiagnostic   `json:"compact_summary,omitempty"`
}

type auditExecutionEventView struct {
	ID             string         `json:"id"`
	ResponseID     string         `json:"response_id,omitempty"`
	RequestAuditID string         `json:"request_audit_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	EventType      string         `json:"event_type"`
	Phase          string         `json:"phase,omitempty"`
	Status         string         `json:"status,omitempty"`
	Message        string         `json:"message,omitempty"`
	DetailsJSON    map[string]any `json:"details_json,omitempty"`
	OccurredAt     time.Time      `json:"occurred_at"`
}

type auditUpstreamExchange struct {
	ID             string    `json:"id"`
	ResponseID     string    `json:"response_id,omitempty"`
	RequestAuditID string    `json:"request_audit_id,omitempty"`
	TraceID        string    `json:"trace_id,omitempty"`
	CassettePath   string    `json:"cassette_path,omitempty"`
	UpstreamID     string    `json:"upstream_id,omitempty"`
	RouteTarget    string    `json:"route_target,omitempty"`
	Model          string    `json:"model,omitempty"`
	Endpoint       string    `json:"endpoint,omitempty"`
	StatusCode     int       `json:"status_code,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	ErrorText      string    `json:"error_text,omitempty"`
}

type auditToolCallEventReference struct {
	ID         string    `json:"id"`
	Status     string    `json:"status,omitempty"`
	Message    string    `json:"message,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

type auditToolCallView struct {
	CallID           string                        `json:"call_id,omitempty"`
	ToolName         string                        `json:"tool_name,omitempty"`
	Executor         string                        `json:"executor,omitempty"`
	StatusesSeen     []string                      `json:"statuses_seen"`
	LatestStatus     string                        `json:"latest_status,omitempty"`
	StartedAt        time.Time                     `json:"started_at,omitempty"`
	CompletedAt      time.Time                     `json:"completed_at,omitempty"`
	ErrorText        string                        `json:"error_text,omitempty"`
	ArgumentsSummary string                        `json:"arguments_summary,omitempty"`
	QuerySummary     string                        `json:"query_summary,omitempty"`
	OutputSummary    string                        `json:"output_summary,omitempty"`
	EventCount       int                           `json:"event_count"`
	Events           []auditToolCallEventReference `json:"events,omitempty"`
	RequestAuditID   string                        `json:"request_audit_id,omitempty"`
	ResponseID       string                        `json:"response_id,omitempty"`
	ConversationID   string                        `json:"conversation_id,omitempty"`
}

type auditJSONPayloadSummary struct {
	Present     bool     `json:"present"`
	Type        string   `json:"type,omitempty"`
	Keys        []string `json:"keys,omitempty"`
	FieldCount  int      `json:"field_count,omitempty"`
	ApproxBytes int      `json:"approx_bytes,omitempty"`
}

type auditToolCallAuditView struct {
	ID                  string                  `json:"id"`
	ResponseID          string                  `json:"response_id,omitempty"`
	RequestAuditID      string                  `json:"request_audit_id,omitempty"`
	ConversationID      string                  `json:"conversation_id,omitempty"`
	CallID              string                  `json:"call_id,omitempty"`
	ToolType            string                  `json:"tool_type,omitempty"`
	ToolName            string                  `json:"tool_name,omitempty"`
	Executor            string                  `json:"executor,omitempty"`
	Status              string                  `json:"status,omitempty"`
	Phase               string                  `json:"phase,omitempty"`
	InputJSONSummary    auditJSONPayloadSummary `json:"input_json_summary"`
	OutputJSONSummary   auditJSONPayloadSummary `json:"output_json_summary"`
	MetadataJSONSummary auditJSONPayloadSummary `json:"metadata_json_summary"`
	InputJSON           map[string]any          `json:"input_json,omitempty"`
	OutputJSON          map[string]any          `json:"output_json,omitempty"`
	MetadataJSON        map[string]any          `json:"metadata_json,omitempty"`
	ErrorText           string                  `json:"error_text,omitempty"`
	StartedAt           time.Time               `json:"started_at,omitempty"`
	CompletedAt         time.Time               `json:"completed_at,omitempty"`
	CreatedAt           time.Time               `json:"created_at"`
}

func newAuditCommand(runtime *cliRuntime) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "audit",
		Short:         "Query stored audit traces",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return requireSubcommand(cmd)
		},
	}
	cmd.AddCommand(newAuditQueryCommand(runtime))
	cmd.AddCommand(newAuditToolCallsCommand(runtime))
	return cmd
}

func newAuditQueryCommand(runtime *cliRuntime) *cobra.Command {
	opts := auditQueryOptions{}
	cmd := &cobra.Command{
		Use:     "query",
		Aliases: []string{"responses"},
		Short:   "Query a stored Responses audit trace",
		Long: "Query a stored Responses audit trace by response id, request audit id, client request id, or conversation id.\n" +
			"Multiple selectors are combined with AND semantics. If a selector matches multiple request audits, the latest trace is returned.\n" +
			"By default only the request audit envelope and derived diagnostics are printed; use --include-events, --include-exchanges, and --include-tools to include related diagnostics.\n" +
			"Use --list with conversation or client request selectors to return matching request audit summaries instead of the latest trace.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.configPath = runtime.configPath()
			opts.format = runtime.outputFormat()
			opts.stdout = cmd.OutOrStdout()
			return runAuditQueryWithOptions(opts)
		},
	}
	cmd.Flags().StringVar(&opts.responseID, "response-id", "", "Responses response id to query")
	cmd.Flags().StringVar(&opts.requestAuditID, "request-audit-id", "", "Request audit id to query")
	cmd.Flags().StringVar(&opts.clientRequestID, "client-request-id", "", "Client request id to query")
	cmd.Flags().StringVar(&opts.conversationID, "conversation-id", "", "Conversation id to query")
	cmd.Flags().StringVar(&opts.status, "status", "", "Request audit status to filter with --list; one of: "+strings.Join(responsesaudit.RequestAuditStatusValues(), ", "))
	cmd.Flags().StringVar(&opts.operation, "operation", "", "Responses operation to filter with --list; one of: "+strings.Join(responsesaudit.RequestAuditOperationValues(), ", "))
	cmd.Flags().BoolVar(&opts.includeEvents, "include-events", false, "Include execution events in the output")
	cmd.Flags().BoolVar(&opts.includeExchanges, "include-exchanges", false, "Include upstream exchanges in the output")
	cmd.Flags().BoolVar(&opts.includeTools, "include-tools", false, "Include derived tool-call diagnostics in the output")
	cmd.Flags().BoolVar(&opts.list, "list", false, "List matching request audit summaries instead of returning the latest trace")
	cmd.Flags().IntVar(&opts.limit, "limit", responsesaudit.DefaultAuditQueryLimit, "Maximum request audit summaries, events, tool-call events, and upstream exchanges to return")
	return cmd
}

func newAuditToolCallsCommand(runtime *cliRuntime) *cobra.Command {
	opts := auditToolCallAuditsOptions{}
	cmd := &cobra.Command{
		Use:     "tool-calls",
		Aliases: []string{"tool-call-audits"},
		Short:   "Query stored tool-call audit records",
		Long: "Query durable tool-call audit records by response id, request audit id, conversation id, call id, tool name, or status.\n" +
			"Multiple selectors are combined with AND semantics. JSON output defaults to payload summaries only; use --include-payloads to include raw input_json, output_json, and metadata_json, which may contain sensitive data.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.configPath = runtime.configPath()
			opts.format = runtime.outputFormat()
			opts.stdout = cmd.OutOrStdout()
			return runAuditToolCallAuditsWithOptions(opts)
		},
	}
	cmd.Flags().StringVar(&opts.responseID, "response-id", "", "Responses response id to query")
	cmd.Flags().StringVar(&opts.requestAuditID, "request-audit-id", "", "Request audit id to query")
	cmd.Flags().StringVar(&opts.conversationID, "conversation-id", "", "Conversation id to query")
	cmd.Flags().StringVar(&opts.callID, "call-id", "", "Tool call id to query")
	cmd.Flags().StringVar(&opts.toolName, "tool-name", "", "Tool name to query")
	cmd.Flags().StringVar(&opts.status, "status", "", "Tool-call audit status to query")
	cmd.Flags().BoolVar(&opts.includePayloads, "include-payloads", false, "Include raw input_json, output_json, and metadata_json; may expose sensitive data")
	cmd.Flags().IntVar(&opts.limit, "limit", responsesaudit.DefaultAuditQueryLimit, "Maximum tool-call audit records to return")
	return cmd
}

func runAuditQueryWithOptions(opts auditQueryOptions) error {
	responseID := strings.TrimSpace(opts.responseID)
	requestAuditID := strings.TrimSpace(opts.requestAuditID)
	clientRequestID := strings.TrimSpace(opts.clientRequestID)
	conversationID := strings.TrimSpace(opts.conversationID)
	status, ok := responsesaudit.NormalizeRequestAuditStatus(opts.status)
	if !ok {
		return cliUsageError(fmt.Sprintf("--status must be one of: %s", strings.Join(responsesaudit.RequestAuditStatusValues(), ", ")), "status")
	}
	if status != "" && !opts.list {
		return cliUsageError("--status can only be used with --list", "status")
	}
	operation, ok := responsesaudit.NormalizeRequestAuditOperation(opts.operation)
	if !ok {
		return cliUsageError(fmt.Sprintf("--operation must be one of: %s", strings.Join(responsesaudit.RequestAuditOperationValues(), ", ")), "operation")
	}
	if operation != "" && !opts.list {
		return cliUsageError("--operation can only be used with --list", "operation")
	}
	if responseID == "" && requestAuditID == "" && clientRequestID == "" && conversationID == "" && (!opts.list || status == "" && operation == "") {
		return cliUsageError("--response-id, --request-audit-id, --client-request-id, or --conversation-id is required; --list may also use --status or --operation", "response-id")
	}
	if opts.limit < 0 {
		return cliUsageError("--limit must be greater than or equal to 0", "limit")
	}
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return cliExitError{
			code:     exitCodeAPI,
			category: errorCategoryAPI,
			errCode:  "CONFIG_LOAD_FAILED",
			message:  err.Error(),
		}
	}
	st, err := openApplicationDatabase(cfg)
	if err != nil {
		return cliExitError{
			code:     exitCodeAPI,
			category: errorCategoryAPI,
			errCode:  "STORE_OPEN_FAILED",
			message:  err.Error(),
		}
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Error("Close application store failed", "error", err)
		}
	}()

	queryService := responsesaudit.NewQueryService(st.EntClient())
	result := auditQueryResult{
		Query: auditQuerySelector{
			ResponseID:       responseID,
			RequestAuditID:   requestAuditID,
			ClientRequestID:  clientRequestID,
			ConversationID:   conversationID,
			Status:           status,
			Operation:        operation,
			IncludeEvents:    opts.includeEvents,
			IncludeExchanges: opts.includeExchanges,
			IncludeTools:     opts.includeTools,
			List:             opts.list,
			Limit:            opts.limit,
		},
		Events:            []auditExecutionEventView{},
		UpstreamExchanges: []auditUpstreamExchange{},
		ToolCalls:         []auditToolCallView{},
	}
	if opts.list {
		audits, err := queryService.ListRequestAudits(context.Background(), responsesaudit.ListRequestAuditsParams{
			ResponseID:      responseID,
			RequestAuditID:  requestAuditID,
			ClientRequestID: clientRequestID,
			ConversationID:  conversationID,
			Status:          status,
			Operation:       operation,
			Limit:           opts.limit,
		})
		if err != nil {
			return cliExitError{
				code:     exitCodeAPI,
				category: errorCategoryAPI,
				errCode:  "RESPONSES_AUDIT_QUERY_FAILED",
				message:  err.Error(),
			}
		}
		result.RequestAudits = []auditRequestAuditSummary{}
		for _, audit := range audits {
			result.RequestAudits = append(result.RequestAudits, auditRequestAuditSummaryFromAudit(audit))
		}
		result.Count = len(result.RequestAudits)
		result.Found = result.Count > 0
		if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "audit.query", result, func(w io.Writer) error {
			writeAuditQueryText(w, result)
			return nil
		}); err != nil {
			return cliExitError{
				code:     exitCodeInternal,
				category: errorCategoryInternal,
				errCode:  "OUTPUT_WRITE_FAILED",
				message:  err.Error(),
			}
		}
		return nil
	}

	trace, found, err := queryService.GetRequestAuditTrace(context.Background(), responsesaudit.GetRequestAuditTraceParams{
		ResponseID:            responseID,
		RequestAuditID:        requestAuditID,
		ClientRequestID:       clientRequestID,
		ConversationID:        conversationID,
		EventLimit:            opts.limit,
		UpstreamExchangeLimit: opts.limit,
	})
	if err != nil {
		return cliExitError{
			code:     exitCodeAPI,
			category: errorCategoryAPI,
			errCode:  "RESPONSES_AUDIT_QUERY_FAILED",
			message:  err.Error(),
		}
	}
	result.Found = found
	if !found {
		return cliExitError{
			code:     exitCodeAPI,
			category: errorCategoryAPI,
			errCode:  "RESPONSES_AUDIT_NOT_FOUND",
			message:  "responses audit trace not found",
		}
	}
	result.RequestAudit = auditRequestAuditFromAudit(trace.RequestAudit)
	result.Diagnostics = auditDiagnosticsFromAudit(trace.Diagnostics)
	if opts.includeEvents {
		for _, event := range trace.ExecutionEvents {
			result.Events = append(result.Events, auditExecutionEventFromAudit(event))
		}
	}
	if opts.includeExchanges {
		for _, exchange := range trace.UpstreamExchanges {
			result.UpstreamExchanges = append(result.UpstreamExchanges, auditUpstreamExchangeFromAudit(exchange))
		}
	}
	if opts.includeTools {
		for _, toolCall := range trace.ToolCalls {
			result.ToolCalls = append(result.ToolCalls, auditToolCallFromAudit(toolCall))
		}
	}
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "audit.query", result, func(w io.Writer) error {
		writeAuditQueryText(w, result)
		return nil
	}); err != nil {
		return cliExitError{
			code:     exitCodeInternal,
			category: errorCategoryInternal,
			errCode:  "OUTPUT_WRITE_FAILED",
			message:  err.Error(),
		}
	}
	return nil
}

func runAuditToolCallAuditsWithOptions(opts auditToolCallAuditsOptions) error {
	responseID := strings.TrimSpace(opts.responseID)
	requestAuditID := strings.TrimSpace(opts.requestAuditID)
	conversationID := strings.TrimSpace(opts.conversationID)
	callID := strings.TrimSpace(opts.callID)
	toolName := strings.TrimSpace(opts.toolName)
	status := strings.TrimSpace(opts.status)
	if opts.limit < 0 {
		return cliUsageError("--limit must be greater than or equal to 0", "limit")
	}
	cfg, err := config.Load(opts.configPath)
	if err != nil {
		return cliExitError{
			code:     exitCodeAPI,
			category: errorCategoryAPI,
			errCode:  "CONFIG_LOAD_FAILED",
			message:  err.Error(),
		}
	}
	st, err := openApplicationDatabase(cfg)
	if err != nil {
		return cliExitError{
			code:     exitCodeAPI,
			category: errorCategoryAPI,
			errCode:  "STORE_OPEN_FAILED",
			message:  err.Error(),
		}
	}
	defer func() {
		if err := st.Close(); err != nil {
			slog.Error("Close application store failed", "error", err)
		}
	}()

	records, err := responsesaudit.NewQueryService(st.EntClient()).ListToolCallAudits(context.Background(), responsesaudit.ListToolCallAuditsParams{
		ResponseID:     responseID,
		RequestAuditID: requestAuditID,
		ConversationID: conversationID,
		CallID:         callID,
		ToolName:       toolName,
		Status:         status,
		Limit:          opts.limit,
	})
	if err != nil {
		return cliExitError{
			code:     exitCodeAPI,
			category: errorCategoryAPI,
			errCode:  "TOOL_CALL_AUDITS_QUERY_FAILED",
			message:  err.Error(),
		}
	}
	result := auditToolCallAuditsResult{
		Query: auditToolCallAuditsSelector{
			ResponseID:      responseID,
			RequestAuditID:  requestAuditID,
			ConversationID:  conversationID,
			CallID:          callID,
			ToolName:        toolName,
			Status:          status,
			IncludePayloads: opts.includePayloads,
			Limit:           opts.limit,
		},
		ToolCallAudits: []auditToolCallAuditView{},
	}
	for _, record := range records {
		result.ToolCallAudits = append(result.ToolCallAudits, auditToolCallAuditFromAudit(record, opts.includePayloads))
	}
	result.Count = len(result.ToolCallAudits)
	if err := writeCLIResult(stdoutOrDefault(opts.stdout), opts.format, "audit.tool_calls", result, func(w io.Writer) error {
		writeAuditToolCallAuditsText(w, result)
		return nil
	}); err != nil {
		return cliExitError{
			code:     exitCodeInternal,
			category: errorCategoryInternal,
			errCode:  "OUTPUT_WRITE_FAILED",
			message:  err.Error(),
		}
	}
	return nil
}

func auditRequestAuditFromAudit(audit responsesaudit.RequestAuditView) *auditRequestAuditView {
	return &auditRequestAuditView{
		ID:              audit.ID,
		ResponseID:      audit.ResponseID,
		ConversationID:  audit.ConversationID,
		Method:          audit.Method,
		Path:            audit.Path,
		Operation:       audit.Operation,
		ClientRequestID: audit.ClientRequestID,
		HeaderJSON:      redactAuditHeaderJSON(audit.HeaderJSON),
		BodyPreview:     audit.BodyPreview,
		BodySHA256:      audit.BodySha256,
		RedactionJSON:   audit.RedactionJSON,
		Status:          audit.Status,
		ErrorText:       audit.ErrorText,
		CreatedAt:       audit.CreatedAt,
	}
}

func auditRequestAuditSummaryFromAudit(audit responsesaudit.RequestAuditView) auditRequestAuditSummary {
	return auditRequestAuditSummary{
		ID:              audit.ID,
		ResponseID:      audit.ResponseID,
		ConversationID:  audit.ConversationID,
		ClientRequestID: audit.ClientRequestID,
		Status:          audit.Status,
		Operation:       audit.Operation,
		CreatedAt:       audit.CreatedAt,
	}
}

func auditDiagnosticsFromAudit(diagnostics responsesaudit.RequestAuditDiagnostics) *auditDiagnosticsView {
	pending := make([]auditPendingToolCallDiagnostic, 0, len(diagnostics.PendingToolCalls))
	for _, toolCall := range diagnostics.PendingToolCalls {
		pending = append(pending, auditPendingToolCallDiagnostic{
			CallID:       toolCall.CallID,
			ToolName:     toolCall.ToolName,
			Executor:     toolCall.Executor,
			LatestStatus: toolCall.LatestStatus,
			StatusesSeen: append([]string(nil), toolCall.StatusesSeen...),
		})
	}
	var compactSummary *auditCompactSummaryDiagnostic
	if diagnostics.CompactSummary != nil {
		compactSummary = &auditCompactSummaryDiagnostic{
			EventCount:       diagnostics.CompactSummary.EventCount,
			AutoTriggered:    diagnostics.CompactSummary.AutoTriggered,
			LatestEventID:    diagnostics.CompactSummary.LatestEventID,
			LatestStatus:     diagnostics.CompactSummary.LatestStatus,
			LatestOccurredAt: diagnostics.CompactSummary.LatestOccurredAt,
		}
	}
	return &auditDiagnosticsView{
		EventCount:            diagnostics.EventCount,
		UpstreamExchangeCount: diagnostics.UpstreamExchangeCount,
		ToolCallCount:         diagnostics.ToolCallCount,
		LatestStatus:          diagnostics.LatestStatus,
		HasCancelled:          diagnostics.HasCancelled,
		HasFailed:             diagnostics.HasFailed,
		HasStreamEvents:       diagnostics.HasStreamEvents,
		HasCompactEvents:      diagnostics.HasCompactEvents,
		PendingToolCallCount:  diagnostics.PendingToolCallCount,
		PendingToolCalls:      pending,
		CompactCandidate:      diagnostics.CompactCandidate,
		CompactSummary:        compactSummary,
	}
}

func redactAuditHeaderJSON(headers map[string]any) map[string]any {
	if len(headers) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(headers))
	for key, value := range headers {
		if auditHeaderSecretKey(key) {
			out[key] = "<redacted>"
			continue
		}
		out[key] = value
	}
	return out
}

func auditHeaderSecretKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key":
		return true
	default:
		return strings.Contains(normalized, "token") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "credential")
	}
}

func auditExecutionEventFromAudit(event responsesaudit.ExecutionEventView) auditExecutionEventView {
	return auditExecutionEventView{
		ID:             event.ID,
		ResponseID:     event.ResponseID,
		RequestAuditID: event.RequestAuditID,
		ConversationID: event.ConversationID,
		EventType:      event.EventType,
		Phase:          event.Phase,
		Status:         event.Status,
		Message:        event.Message,
		DetailsJSON:    event.DetailsJSON,
		OccurredAt:     event.OccurredAt,
	}
}

func auditUpstreamExchangeFromAudit(exchange responsesaudit.UpstreamExchangeView) auditUpstreamExchange {
	return auditUpstreamExchange{
		ID:             exchange.ID,
		ResponseID:     exchange.ResponseID,
		RequestAuditID: exchange.RequestAuditID,
		TraceID:        exchange.TraceID,
		CassettePath:   exchange.CassettePath,
		UpstreamID:     exchange.UpstreamID,
		RouteTarget:    exchange.RouteTarget,
		Model:          exchange.Model,
		Endpoint:       exchange.Endpoint,
		StatusCode:     exchange.StatusCode,
		StartedAt:      exchange.StartedAt,
		CompletedAt:    exchange.CompletedAt,
		ErrorText:      exchange.ErrorText,
	}
}

func auditToolCallFromAudit(toolCall responsesaudit.ToolCallView) auditToolCallView {
	events := make([]auditToolCallEventReference, 0, len(toolCall.Events))
	for _, event := range toolCall.Events {
		events = append(events, auditToolCallEventReference{
			ID:         event.ID,
			Status:     event.Status,
			Message:    event.Message,
			OccurredAt: event.OccurredAt,
		})
	}
	return auditToolCallView{
		CallID:           toolCall.CallID,
		ToolName:         toolCall.ToolName,
		Executor:         toolCall.Executor,
		StatusesSeen:     append([]string(nil), toolCall.StatusesSeen...),
		LatestStatus:     toolCall.LatestStatus,
		StartedAt:        toolCall.StartedAt,
		CompletedAt:      toolCall.CompletedAt,
		ErrorText:        toolCall.ErrorText,
		ArgumentsSummary: toolCall.ArgumentsSummary,
		QuerySummary:     toolCall.QuerySummary,
		OutputSummary:    toolCall.OutputSummary,
		EventCount:       toolCall.EventCount,
		Events:           events,
		RequestAuditID:   toolCall.RequestAuditID,
		ResponseID:       toolCall.ResponseID,
		ConversationID:   toolCall.ConversationID,
	}
}

func auditToolCallAuditFromAudit(record responsesaudit.ToolCallAuditView, includePayloads bool) auditToolCallAuditView {
	view := auditToolCallAuditView{
		ID:                  record.ID,
		ResponseID:          record.ResponseID,
		RequestAuditID:      record.RequestAuditID,
		ConversationID:      record.ConversationID,
		CallID:              record.CallID,
		ToolType:            record.ToolType,
		ToolName:            record.ToolName,
		Executor:            record.Executor,
		Status:              record.Status,
		Phase:               record.Phase,
		InputJSONSummary:    auditJSONSummary(record.InputJSON),
		OutputJSONSummary:   auditJSONSummary(record.OutputJSON),
		MetadataJSONSummary: auditJSONSummary(record.MetadataJSON),
		ErrorText:           record.ErrorText,
		StartedAt:           record.StartedAt,
		CompletedAt:         record.CompletedAt,
		CreatedAt:           record.CreatedAt,
	}
	if includePayloads {
		view.InputJSON = record.InputJSON
		view.OutputJSON = record.OutputJSON
		view.MetadataJSON = record.MetadataJSON
	}
	return view
}

func auditJSONSummary(payload map[string]any) auditJSONPayloadSummary {
	if len(payload) == 0 {
		return auditJSONPayloadSummary{Present: false, Type: "object", Keys: []string{}, FieldCount: 0}
	}
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	encoded, _ := json.Marshal(payload)
	return auditJSONPayloadSummary{
		Present:     true,
		Type:        "object",
		Keys:        keys,
		FieldCount:  len(payload),
		ApproxBytes: len(encoded),
	}
}

func writeAuditQueryText(w io.Writer, result auditQueryResult) {
	if result.Query.List {
		fmt.Fprintf(w, "request_audits: %d\n", result.Count)
		for _, audit := range result.RequestAudits {
			fmt.Fprintf(w, "- %s", audit.ID)
			if audit.ResponseID != "" {
				fmt.Fprintf(w, " response_id=%s", audit.ResponseID)
			}
			if audit.ConversationID != "" {
				fmt.Fprintf(w, " conversation_id=%s", audit.ConversationID)
			}
			if audit.ClientRequestID != "" {
				fmt.Fprintf(w, " client_request_id=%s", audit.ClientRequestID)
			}
			if audit.Status != "" {
				fmt.Fprintf(w, " status=%s", audit.Status)
			}
			if audit.Operation != "" {
				fmt.Fprintf(w, " operation=%s", audit.Operation)
			}
			if !audit.CreatedAt.IsZero() {
				fmt.Fprintf(w, " created_at=%s", audit.CreatedAt.Format(time.RFC3339))
			}
			fmt.Fprintln(w)
		}
		return
	}
	if result.RequestAudit == nil {
		fmt.Fprintln(w, "responses audit trace not found")
		return
	}
	audit := result.RequestAudit
	fmt.Fprintf(w, "request_audit_id: %s\n", audit.ID)
	fmt.Fprintf(w, "response_id: %s\n", audit.ResponseID)
	fmt.Fprintf(w, "status: %s\n", audit.Status)
	fmt.Fprintf(w, "method: %s\n", audit.Method)
	fmt.Fprintf(w, "path: %s\n", audit.Path)
	if audit.Operation != "" {
		fmt.Fprintf(w, "operation: %s\n", audit.Operation)
	}
	if audit.ClientRequestID != "" {
		fmt.Fprintf(w, "client_request_id: %s\n", audit.ClientRequestID)
	}
	if audit.BodyPreview != "" {
		fmt.Fprintf(w, "body_preview: %s\n", audit.BodyPreview)
	}
	if result.Diagnostics != nil {
		fmt.Fprintf(w, "diagnostics: events=%d upstream_exchanges=%d tool_calls=%d latest_status=%s cancelled=%t failed=%t stream=%t compact=%t pending_tool_calls=%d compact_candidate=%t\n",
			result.Diagnostics.EventCount,
			result.Diagnostics.UpstreamExchangeCount,
			result.Diagnostics.ToolCallCount,
			result.Diagnostics.LatestStatus,
			result.Diagnostics.HasCancelled,
			result.Diagnostics.HasFailed,
			result.Diagnostics.HasStreamEvents,
			result.Diagnostics.HasCompactEvents,
			result.Diagnostics.PendingToolCallCount,
			result.Diagnostics.CompactCandidate,
		)
	}
	if result.Query.IncludeEvents {
		fmt.Fprintf(w, "events: %d\n", len(result.Events))
		for _, event := range result.Events {
			fmt.Fprintf(w, "- %s %s %s %s\n", event.ID, event.EventType, event.Phase, event.Status)
		}
	}
	if result.Query.IncludeExchanges {
		fmt.Fprintf(w, "upstream_exchanges: %d\n", len(result.UpstreamExchanges))
		for _, exchange := range result.UpstreamExchanges {
			fmt.Fprintf(w, "- %s %s %s %d %s\n", exchange.ID, exchange.UpstreamID, exchange.Model, exchange.StatusCode, exchange.TraceID)
		}
	}
	if result.Query.IncludeTools {
		fmt.Fprintf(w, "tool_calls: %d\n", len(result.ToolCalls))
		for _, toolCall := range result.ToolCalls {
			fmt.Fprintf(w, "- %s %s %s status=%s events=%d", toolCall.CallID, toolCall.ToolName, toolCall.Executor, toolCall.LatestStatus, toolCall.EventCount)
			if !toolCall.StartedAt.IsZero() {
				fmt.Fprintf(w, " started_at=%s", toolCall.StartedAt.Format(time.RFC3339))
			}
			if !toolCall.CompletedAt.IsZero() {
				fmt.Fprintf(w, " completed_at=%s", toolCall.CompletedAt.Format(time.RFC3339))
			}
			if toolCall.OutputSummary != "" {
				fmt.Fprintf(w, " output=%s", toolCall.OutputSummary)
			}
			if toolCall.ErrorText != "" {
				fmt.Fprintf(w, " error=%s", toolCall.ErrorText)
			}
			fmt.Fprintln(w)
		}
	}
}

func writeAuditToolCallAuditsText(w io.Writer, result auditToolCallAuditsResult) {
	fmt.Fprintf(w, "tool_call_audits: %d\n", result.Count)
	for _, record := range result.ToolCallAudits {
		fmt.Fprintf(w, "- %s", record.ID)
		if record.CallID != "" {
			fmt.Fprintf(w, " call_id=%s", record.CallID)
		}
		if record.ToolName != "" {
			fmt.Fprintf(w, " tool_name=%s", record.ToolName)
		}
		if record.Executor != "" {
			fmt.Fprintf(w, " executor=%s", record.Executor)
		}
		if record.Status != "" {
			fmt.Fprintf(w, " status=%s", record.Status)
		}
		if record.ResponseID != "" {
			fmt.Fprintf(w, " response_id=%s", record.ResponseID)
		}
		if record.RequestAuditID != "" {
			fmt.Fprintf(w, " request_audit_id=%s", record.RequestAuditID)
		}
		if !record.CreatedAt.IsZero() {
			fmt.Fprintf(w, " created_at=%s", record.CreatedAt.Format(time.RFC3339))
		}
		fmt.Fprintln(w)
	}
}
