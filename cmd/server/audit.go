package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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
	includeEvents    bool
	includeExchanges bool
	includeTools     bool
	limit            int
}

type auditQueryResult struct {
	Query             auditQuerySelector        `json:"query"`
	Found             bool                      `json:"found"`
	RequestAudit      *auditRequestAuditView    `json:"request_audit,omitempty"`
	Events            []auditExecutionEventView `json:"events"`
	UpstreamExchanges []auditUpstreamExchange   `json:"upstream_exchanges"`
	ToolCalls         []auditToolCallView       `json:"tool_calls"`
}

type auditQuerySelector struct {
	ResponseID       string `json:"response_id,omitempty"`
	RequestAuditID   string `json:"request_audit_id,omitempty"`
	ClientRequestID  string `json:"client_request_id,omitempty"`
	ConversationID   string `json:"conversation_id,omitempty"`
	IncludeEvents    bool   `json:"include_events"`
	IncludeExchanges bool   `json:"include_exchanges"`
	IncludeTools     bool   `json:"include_tools"`
	Limit            int    `json:"limit,omitempty"`
}

type auditRequestAuditView struct {
	ID              string         `json:"id"`
	ResponseID      string         `json:"response_id,omitempty"`
	ConversationID  string         `json:"conversation_id,omitempty"`
	Method          string         `json:"method"`
	Path            string         `json:"path"`
	ClientRequestID string         `json:"client_request_id,omitempty"`
	HeaderJSON      map[string]any `json:"header_json,omitempty"`
	BodyPreview     string         `json:"body_preview,omitempty"`
	BodySHA256      string         `json:"body_sha256,omitempty"`
	RedactionJSON   map[string]any `json:"redaction_json,omitempty"`
	Status          string         `json:"status,omitempty"`
	ErrorText       string         `json:"error_text,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
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
			"By default only the request audit envelope is printed; use --include-events, --include-exchanges, and --include-tools to include related diagnostics.",
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
	cmd.Flags().BoolVar(&opts.includeEvents, "include-events", false, "Include execution events in the output")
	cmd.Flags().BoolVar(&opts.includeExchanges, "include-exchanges", false, "Include upstream exchanges in the output")
	cmd.Flags().BoolVar(&opts.includeTools, "include-tools", false, "Include derived tool-call diagnostics in the output")
	cmd.Flags().IntVar(&opts.limit, "limit", responsesaudit.DefaultAuditQueryLimit, "Maximum events, tool-call events, and upstream exchanges to return when included")
	return cmd
}

func runAuditQueryWithOptions(opts auditQueryOptions) error {
	responseID := strings.TrimSpace(opts.responseID)
	requestAuditID := strings.TrimSpace(opts.requestAuditID)
	clientRequestID := strings.TrimSpace(opts.clientRequestID)
	conversationID := strings.TrimSpace(opts.conversationID)
	if responseID == "" && requestAuditID == "" && clientRequestID == "" && conversationID == "" {
		return cliUsageError("--response-id, --request-audit-id, --client-request-id, or --conversation-id is required", "response-id")
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

	trace, found, err := responsesaudit.NewQueryService(st.EntClient()).GetRequestAuditTrace(context.Background(), responsesaudit.GetRequestAuditTraceParams{
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
	result := auditQueryResult{
		Query: auditQuerySelector{
			ResponseID:       responseID,
			RequestAuditID:   requestAuditID,
			ClientRequestID:  clientRequestID,
			ConversationID:   conversationID,
			IncludeEvents:    opts.includeEvents,
			IncludeExchanges: opts.includeExchanges,
			IncludeTools:     opts.includeTools,
			Limit:            opts.limit,
		},
		Found:             found,
		Events:            []auditExecutionEventView{},
		UpstreamExchanges: []auditUpstreamExchange{},
		ToolCalls:         []auditToolCallView{},
	}
	if !found {
		return cliExitError{
			code:     exitCodeAPI,
			category: errorCategoryAPI,
			errCode:  "RESPONSES_AUDIT_NOT_FOUND",
			message:  "responses audit trace not found",
		}
	}
	result.RequestAudit = auditRequestAuditFromAudit(trace.RequestAudit)
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

func auditRequestAuditFromAudit(audit responsesaudit.RequestAuditView) *auditRequestAuditView {
	return &auditRequestAuditView{
		ID:              audit.ID,
		ResponseID:      audit.ResponseID,
		ConversationID:  audit.ConversationID,
		Method:          audit.Method,
		Path:            audit.Path,
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

func writeAuditQueryText(w io.Writer, result auditQueryResult) {
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
	if audit.ClientRequestID != "" {
		fmt.Fprintf(w, "client_request_id: %s\n", audit.ClientRequestID)
	}
	if audit.BodyPreview != "" {
		fmt.Fprintf(w, "body_preview: %s\n", audit.BodyPreview)
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
