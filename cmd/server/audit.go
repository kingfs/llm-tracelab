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
	includeEvents    bool
	includeExchanges bool
	limit            int
}

type auditQueryResult struct {
	Query             auditQuerySelector        `json:"query"`
	Found             bool                      `json:"found"`
	RequestAudit      *auditRequestAuditView    `json:"request_audit,omitempty"`
	Events            []auditExecutionEventView `json:"events"`
	UpstreamExchanges []auditUpstreamExchange   `json:"upstream_exchanges"`
}

type auditQuerySelector struct {
	ResponseID       string `json:"response_id,omitempty"`
	RequestAuditID   string `json:"request_audit_id,omitempty"`
	IncludeEvents    bool   `json:"include_events"`
	IncludeExchanges bool   `json:"include_exchanges"`
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
		Long: "Query a stored Responses audit trace by response id or request audit id.\n" +
			"By default only the request audit envelope is printed; use --include-events and --include-exchanges to include related rows.",
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
	cmd.Flags().BoolVar(&opts.includeEvents, "include-events", false, "Include execution events in the output")
	cmd.Flags().BoolVar(&opts.includeExchanges, "include-exchanges", false, "Include upstream exchanges in the output")
	cmd.Flags().IntVar(&opts.limit, "limit", responsesaudit.DefaultAuditQueryLimit, "Maximum events and upstream exchanges to return when included")
	return cmd
}

func runAuditQueryWithOptions(opts auditQueryOptions) error {
	responseID := strings.TrimSpace(opts.responseID)
	requestAuditID := strings.TrimSpace(opts.requestAuditID)
	if responseID == "" && requestAuditID == "" {
		return cliUsageError("--response-id or --request-audit-id is required", "response-id")
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
			IncludeEvents:    opts.includeEvents,
			IncludeExchanges: opts.includeExchanges,
			Limit:            opts.limit,
		},
		Found:             found,
		Events:            []auditExecutionEventView{},
		UpstreamExchanges: []auditUpstreamExchange{},
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
	result.Query.ResponseID = trace.RequestAudit.ResponseID
	result.Query.RequestAuditID = trace.RequestAudit.ID
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
		HeaderJSON:      audit.HeaderJSON,
		BodyPreview:     audit.BodyPreview,
		BodySHA256:      audit.BodySha256,
		RedactionJSON:   audit.RedactionJSON,
		Status:          audit.Status,
		ErrorText:       audit.ErrorText,
		CreatedAt:       audit.CreatedAt,
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
}
