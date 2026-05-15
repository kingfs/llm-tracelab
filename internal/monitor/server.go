package monitor

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/channel"
	"github.com/kingfs/llm-tracelab/internal/reanalysis"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/upstream"
	"github.com/kingfs/llm-tracelab/pkg/observe"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

//go:embed ui/dist/*
var uiFS embed.FS

type listResponse struct {
	Items       []traceListItem `json:"items"`
	Stats       LogStats        `json:"stats"`
	Page        int             `json:"page"`
	PageSize    int             `json:"page_size"`
	Total       int             `json:"total"`
	TotalPages  int             `json:"total_pages"`
	RefreshedAt time.Time       `json:"refreshed_at"`
}

type overviewResponse struct {
	Window      string                  `json:"window"`
	RefreshedAt time.Time               `json:"refreshed_at"`
	Summary     overviewSummaryView     `json:"summary"`
	Timeline    []overviewTimelineItem  `json:"timeline"`
	Breakdown   overviewBreakdownView   `json:"breakdown"`
	Attention   overviewAttentionView   `json:"attention"`
	Analysis    overviewAnalysisSummary `json:"analysis"`
	Observation overviewObservationView `json:"observation"`
}

type overviewSummaryView struct {
	RequestCount   int     `json:"request_count"`
	SuccessRequest int     `json:"success_request"`
	FailedRequest  int     `json:"failed_request"`
	SuccessRate    float64 `json:"success_rate"`
	TotalTokens    int     `json:"total_tokens"`
	AvgTTFTMs      int     `json:"avg_ttft_ms"`
	AvgDurationMs  int64   `json:"avg_duration_ms"`
	P95TTFTMs      int     `json:"p95_ttft_ms"`
	P95DurationMs  int64   `json:"p95_duration_ms"`
	StreamCount    int     `json:"stream_count"`
	SessionCount   int     `json:"session_count"`
}

type overviewTimelineItem struct {
	Time          time.Time `json:"time"`
	RequestCount  int       `json:"request_count"`
	FailedRequest int       `json:"failed_request"`
	TotalTokens   int       `json:"total_tokens"`
	AvgTTFTMs     int       `json:"avg_ttft_ms"`
	AvgDurationMs int64     `json:"avg_duration_ms"`
}

type overviewBreakdownView struct {
	Models                []sessionCountItem `json:"models"`
	Providers             []sessionCountItem `json:"providers"`
	Endpoints             []sessionCountItem `json:"endpoints"`
	Upstreams             []sessionCountItem `json:"upstreams"`
	RoutingFailureReasons []sessionCountItem `json:"routing_failure_reasons"`
	FindingCategories     []sessionCountItem `json:"finding_categories"`
}

type overviewAttentionView struct {
	RecentFailures   []traceListItem      `json:"recent_failures"`
	HighRiskFindings []findingView        `json:"high_risk_findings"`
	RoutingFailures  []routingFailureItem `json:"routing_failures"`
	SlowTraces       []traceListItem      `json:"slow_traces"`
}

type overviewAnalysisSummary struct {
	Total  int               `json:"total"`
	Failed int               `json:"failed"`
	Recent []analysisRunView `json:"recent"`
}

type overviewObservationView struct {
	TotalObservations int                `json:"total_observations"`
	Parsed            int                `json:"parsed"`
	Failed            int                `json:"failed"`
	Queued            int                `json:"queued"`
	Running           int                `json:"running"`
	Unparsed          int                `json:"unparsed"`
	RecentFailures    []overviewParseJob `json:"recent_failures"`
}

type overviewParseJob struct {
	ID        int64     `json:"id"`
	TraceID   string    `json:"trace_id"`
	Status    string    `json:"status"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type systemEventListResponse struct {
	Items       []systemEventView `json:"items"`
	Page        int               `json:"page"`
	PageSize    int               `json:"page_size"`
	Total       int               `json:"total"`
	TotalPages  int               `json:"total_pages"`
	Window      string            `json:"window"`
	RefreshedAt time.Time         `json:"refreshed_at"`
}

type systemEventSummaryResponse struct {
	Total      int                `json:"total"`
	Unread     int                `json:"unread"`
	Critical   int                `json:"critical"`
	Error      int                `json:"error"`
	Warning    int                `json:"warning"`
	LastSeenAt *time.Time         `json:"last_seen_at,omitempty"`
	BySource   []sessionCountItem `json:"by_source"`
	ByCategory []sessionCountItem `json:"by_category"`
	Window     string             `json:"window"`
}

type systemEventStreamMessage struct {
	Type       string                     `json:"type"`
	EventID    string                     `json:"event_id,omitempty"`
	Status     string                     `json:"status,omitempty"`
	Severity   string                     `json:"severity,omitempty"`
	Source     string                     `json:"source,omitempty"`
	Category   string                     `json:"category,omitempty"`
	Summary    systemEventSummaryResponse `json:"summary"`
	Unread     int                        `json:"unread"`
	LastSeenAt *time.Time                 `json:"last_seen_at,omitempty"`
}

type systemEventView struct {
	ID              string          `json:"id"`
	Fingerprint     string          `json:"fingerprint"`
	Source          string          `json:"source"`
	Category        string          `json:"category"`
	Severity        string          `json:"severity"`
	Status          string          `json:"status"`
	Title           string          `json:"title"`
	Message         string          `json:"message"`
	Details         json.RawMessage `json:"details_json"`
	TraceID         string          `json:"trace_id,omitempty"`
	SessionID       string          `json:"session_id,omitempty"`
	JobID           string          `json:"job_id,omitempty"`
	UpstreamID      string          `json:"upstream_id,omitempty"`
	Model           string          `json:"model,omitempty"`
	OccurrenceCount int             `json:"occurrence_count"`
	FirstSeenAt     time.Time       `json:"first_seen_at"`
	LastSeenAt      time.Time       `json:"last_seen_at"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	ReadAt          *time.Time      `json:"read_at,omitempty"`
	ResolvedAt      *time.Time      `json:"resolved_at,omitempty"`
}

type traceListItem struct {
	ID               string    `json:"id"`
	SessionID        string    `json:"session_id,omitempty"`
	SessionSource    string    `json:"session_source,omitempty"`
	RecordedAt       time.Time `json:"recorded_at"`
	Model            string    `json:"model"`
	Provider         string    `json:"provider"`
	SelectedUpstream string    `json:"selected_upstream_id,omitempty"`
	Operation        string    `json:"operation"`
	Endpoint         string    `json:"endpoint"`
	Method           string    `json:"method"`
	URL              string    `json:"url"`
	StatusCode       int       `json:"status_code"`
	DurationMs       int64     `json:"duration_ms"`
	TTFTMs           int64     `json:"ttft_ms"`
	TotalTokens      int       `json:"total_tokens"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	CachedTokens     int       `json:"cached_tokens"`
	IsStream         bool      `json:"is_stream"`
	Error            string    `json:"error,omitempty"`
}

type sessionListResponse struct {
	Items       []sessionListItem `json:"items"`
	Page        int               `json:"page"`
	PageSize    int               `json:"page_size"`
	Total       int               `json:"total"`
	TotalPages  int               `json:"total_pages"`
	RefreshedAt time.Time         `json:"refreshed_at"`
}

type sessionListItem struct {
	SessionID      string    `json:"session_id"`
	SessionSource  string    `json:"session_source"`
	RequestCount   int       `json:"request_count"`
	FirstSeen      time.Time `json:"first_seen"`
	LastSeen       time.Time `json:"last_seen"`
	LastModel      string    `json:"last_model"`
	Providers      []string  `json:"providers"`
	SuccessRequest int       `json:"success_request"`
	FailedRequest  int       `json:"failed_request"`
	SuccessRate    float64   `json:"success_rate"`
	TotalTokens    int       `json:"total_tokens"`
	AvgTTFT        int       `json:"avg_ttft"`
	TotalDuration  int64     `json:"total_duration_ms"`
	StreamCount    int       `json:"stream_count"`
}

type sessionDetailResponse struct {
	Summary     sessionListItem       `json:"summary"`
	Breakdown   sessionBreakdownView  `json:"breakdown"`
	Timeline    []sessionTimelineItem `json:"timeline"`
	Performance performanceView       `json:"performance"`
	Analysis    []analysisRunView     `json:"analysis"`
	Traces      []traceListItem       `json:"traces"`
}

type analysisListResponse struct {
	SessionID string            `json:"session_id,omitempty"`
	TraceID   string            `json:"trace_id,omitempty"`
	Items     []analysisRunView `json:"items"`
	Total     int               `json:"total"`
}

type analysisRunView struct {
	ID              int64     `json:"id"`
	TraceID         string    `json:"trace_id,omitempty"`
	SessionID       string    `json:"session_id,omitempty"`
	Kind            string    `json:"kind"`
	Analyzer        string    `json:"analyzer"`
	AnalyzerVersion string    `json:"analyzer_version"`
	Model           string    `json:"model,omitempty"`
	InputRef        string    `json:"input_ref"`
	Output          any       `json:"output"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

type analysisJobListResponse struct {
	Items []analysisJobView `json:"items"`
	Total int               `json:"total"`
}

type analysisJobResponse struct {
	Job analysisJobView `json:"job"`
}

type reanalysisResponse struct {
	Job    analysisJobView `json:"job"`
	Result any             `json:"result,omitempty"`
}

type reanalysisRequest struct {
	Mode            string `json:"mode"`
	Scan            bool   `json:"scan"`
	RewriteCassette bool   `json:"rewrite_cassette"`
	Reparse         bool   `json:"reparse"`
}

type batchReanalysisRequest struct {
	Mode         string `json:"mode"`
	Query        string `json:"q"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Endpoint     string `json:"endpoint"`
	Upstream     string `json:"upstream"`
	Status       string `json:"status"`
	MissingUsage bool   `json:"missing_usage"`
	Limit        int    `json:"limit"`
	RepairUsage  bool   `json:"repair_usage"`
	Reparse      bool   `json:"reparse"`
	Scan         bool   `json:"scan"`
}

type analysisJobView struct {
	ID         int64     `json:"id"`
	JobType    string    `json:"job_type"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Status     string    `json:"status"`
	Steps      []string  `json:"steps"`
	Request    any       `json:"request"`
	Result     any       `json:"result"`
	LastError  string    `json:"last_error,omitempty"`
	Attempts   int       `json:"attempts"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

type sessionBreakdownView struct {
	Models       []sessionCountItem `json:"models"`
	Endpoints    []sessionCountItem `json:"endpoints"`
	FailedTraces int                `json:"failed_traces"`
}

type providerPresetResponse struct {
	Items    []string                   `json:"items"`
	Presets  []providerPresetItem       `json:"presets"`
	Defaults providerPresetFormDefaults `json:"defaults"`
}

type providerPresetItem struct {
	ID              string   `json:"id"`
	ProtocolFamily  string   `json:"protocol_family"`
	RoutingProfile  string   `json:"routing_profile"`
	SupportLevel    string   `json:"support_level"`
	AllowedProfiles []string `json:"allowed_profiles"`
}

type providerPresetFormDefaults struct {
	ProtocolFamilies []string `json:"protocol_families"`
	RoutingProfiles  []string `json:"routing_profiles"`
	ModelDiscovery   []string `json:"model_discovery"`
}

type sessionCountItem struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type sessionTimelineItem struct {
	TraceID     string    `json:"trace_id"`
	Time        time.Time `json:"time"`
	Model       string    `json:"model"`
	Provider    string    `json:"provider"`
	Endpoint    string    `json:"endpoint"`
	StatusCode  int       `json:"status_code"`
	DurationMs  int64     `json:"duration_ms"`
	TTFTMs      int64     `json:"ttft_ms"`
	TotalTokens int       `json:"total_tokens"`
	IsStream    bool      `json:"is_stream"`
	Error       string    `json:"error,omitempty"`
}

type detailResponse struct {
	ID                     string                   `json:"id"`
	Session                *traceSessionView        `json:"session,omitempty"`
	Header                 recordHeaderView         `json:"header"`
	Events                 []recordEventView        `json:"events"`
	Messages               []ChatMessage            `json:"messages"`
	Tools                  []RequestTool            `json:"tools"`
	AIContent              string                   `json:"ai_content"`
	AIReasoning            string                   `json:"ai_reasoning"`
	AIBlocks               []ContentBlock           `json:"ai_blocks"`
	ToolCalls              []ToolCall               `json:"tool_calls"`
	SelectedUpstreamHealth *traceUpstreamHealthView `json:"selected_upstream_health,omitempty"`
	Performance            performanceView          `json:"performance"`
}

type performanceResponse struct {
	ID          string          `json:"id"`
	Scope       string          `json:"scope"`
	Performance performanceView `json:"performance"`
}

type performanceView struct {
	RequestCount       int             `json:"request_count"`
	SuccessRequest     int             `json:"success_request"`
	FailedRequest      int             `json:"failed_request"`
	SuccessRate        float64         `json:"success_rate"`
	DurationMs         int64           `json:"duration_ms"`
	TTFTMs             int64           `json:"ttft_ms"`
	TokensPerSec       float64         `json:"tokens_per_sec"`
	TotalTokens        int             `json:"total_tokens"`
	PromptTokens       int             `json:"prompt_tokens"`
	CompletionTokens   int             `json:"completion_tokens"`
	CachedTokens       int             `json:"cached_tokens"`
	CacheRatio         float64         `json:"cache_ratio"`
	StatusCode         int             `json:"status_code,omitempty"`
	ProviderError      string          `json:"provider_error,omitempty"`
	IsStream           bool            `json:"is_stream,omitempty"`
	SelectedUpstreamID string          `json:"selected_upstream_id,omitempty"`
	RoutingPolicy      string          `json:"routing_policy,omitempty"`
	RoutingFallback    bool            `json:"routing_fallback,omitempty"`
	Upstreams          []upstreamPerf  `json:"upstreams,omitempty"`
	ByModel            []perfCountItem `json:"by_model,omitempty"`
	ByEndpoint         []perfCountItem `json:"by_endpoint,omitempty"`
}

type upstreamPerf struct {
	ID             string  `json:"id"`
	BaseURL        string  `json:"base_url,omitempty"`
	ProviderPreset string  `json:"provider_preset,omitempty"`
	RequestCount   int     `json:"request_count"`
	SuccessRequest int     `json:"success_request"`
	FailedRequest  int     `json:"failed_request"`
	SuccessRate    float64 `json:"success_rate"`
	TotalTokens    int     `json:"total_tokens"`
	AvgTTFT        int     `json:"avg_ttft"`
	HealthState    string  `json:"health_state,omitempty"`
	ErrorRate      float64 `json:"error_rate,omitempty"`
	TimeoutRate    float64 `json:"timeout_rate,omitempty"`
}

type perfCountItem struct {
	Label        string  `json:"label"`
	Count        int     `json:"count"`
	TotalTokens  int     `json:"total_tokens"`
	AvgDuration  int64   `json:"avg_duration_ms"`
	AvgTTFT      int64   `json:"avg_ttft_ms"`
	SuccessRate  float64 `json:"success_rate"`
	TokensPerSec float64 `json:"tokens_per_sec"`
}

type traceUpstreamHealthView struct {
	ID                string              `json:"id"`
	HealthState       string              `json:"health_state"`
	TTFTFastMs        float64             `json:"ttft_fast_ms"`
	TTFTSlowMs        float64             `json:"ttft_slow_ms"`
	LatencyFastMs     float64             `json:"latency_fast_ms"`
	ErrorRate         float64             `json:"error_rate"`
	TimeoutRate       float64             `json:"timeout_rate"`
	Inflight          int64               `json:"inflight"`
	LastRefreshAt     time.Time           `json:"last_refresh_at"`
	LastRefreshStatus string              `json:"last_refresh_status"`
	HealthThresholds  healthThresholdView `json:"health_thresholds"`
}

type rawDetailResponse struct {
	ID               string            `json:"id"`
	RequestProtocol  string            `json:"request_protocol"`
	ResponseProtocol string            `json:"response_protocol"`
	Header           recordHeaderView  `json:"header"`
	Events           []recordEventView `json:"events"`
}

type observationDetailResponse struct {
	ID      string                 `json:"id"`
	Summary observationSummaryView `json:"summary"`
	Nodes   []observationNodeView  `json:"nodes"`
	Tree    []observationNodeView  `json:"tree"`
}

type findingListResponse struct {
	ID       string        `json:"id"`
	Items    []findingView `json:"items"`
	Total    int           `json:"total"`
	Severity string        `json:"severity,omitempty"`
	Category string        `json:"category,omitempty"`
}

type findingView struct {
	ID              string    `json:"id"`
	TraceID         string    `json:"trace_id"`
	Category        string    `json:"category"`
	Severity        string    `json:"severity"`
	Confidence      float64   `json:"confidence"`
	Title           string    `json:"title"`
	Description     string    `json:"description,omitempty"`
	EvidencePath    string    `json:"evidence_path"`
	EvidenceExcerpt string    `json:"evidence_excerpt,omitempty"`
	NodeID          string    `json:"node_id,omitempty"`
	Detector        string    `json:"detector"`
	DetectorVersion string    `json:"detector_version"`
	CreatedAt       time.Time `json:"created_at"`
}

type observationSummaryView struct {
	TraceID       string    `json:"trace_id"`
	Parser        string    `json:"parser"`
	ParserVersion string    `json:"parser_version"`
	Status        string    `json:"status"`
	Provider      string    `json:"provider"`
	Operation     string    `json:"operation"`
	Model         string    `json:"model"`
	Summary       any       `json:"summary"`
	Warnings      any       `json:"warnings"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type observationNodeView struct {
	ID             string                `json:"id"`
	ParentID       string                `json:"parent_id,omitempty"`
	ProviderType   string                `json:"provider_type"`
	NormalizedType string                `json:"normalized_type"`
	Role           string                `json:"role,omitempty"`
	Path           string                `json:"path"`
	Index          int                   `json:"index"`
	Depth          int                   `json:"depth,omitempty"`
	TextPreview    string                `json:"text_preview,omitempty"`
	Raw            json.RawMessage       `json:"raw,omitempty"`
	Children       []observationNodeView `json:"children,omitempty"`
}

type traceSessionView struct {
	SessionID     string `json:"session_id"`
	SessionSource string `json:"session_source"`
}

type recordHeaderView struct {
	Version string      `json:"version"`
	Meta    interface{} `json:"meta"`
	Layout  interface{} `json:"layout"`
	Usage   interface{} `json:"usage"`
}

type recordEventView map[string]interface{}

type timelineItemView struct {
	Kind     string             `json:"kind"`
	Label    string             `json:"label,omitempty"`
	Summary  string             `json:"summary,omitempty"`
	Body     string             `json:"body,omitempty"`
	Role     string             `json:"role,omitempty"`
	Name     string             `json:"name,omitempty"`
	ID       string             `json:"id,omitempty"`
	Status   string             `json:"status,omitempty"`
	Children []timelineItemView `json:"children,omitempty"`
}

type LogStats struct {
	TotalRequest   int     `json:"total_request"`
	AvgTTFT        int     `json:"avg_ttft"`
	TotalTokens    int     `json:"total_tokens"`
	SuccessRequest int     `json:"success_request"`
	FailedRequest  int     `json:"failed_request"`
	SuccessRate    float64 `json:"success_rate"`
}

type RouteOptions struct {
	Router         *router.Router
	ChannelService *channel.Service
	AuthVerifier   auth.TokenVerifier
	AuthStore      *auth.Store
	SessionTTL     time.Duration
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token  string `json:"token"`
	Prefix string `json:"prefix"`
}

type meResponse struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Scope    string `json:"scope"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type createTokenRequest struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
	TTL   string `json:"ttl"`
}

type createTokenResponse struct {
	Token  string `json:"token"`
	Prefix string `json:"prefix"`
}

type tokenListResponse struct {
	Items []tokenItem `json:"items"`
	Total int         `json:"total"`
}

type tokenItem struct {
	ID         int        `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scope      string     `json:"scope"`
	Enabled    bool       `json:"enabled"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type upstreamListResponse struct {
	Items           []upstreamItem            `json:"items"`
	RoutingFailures routingFailureSummaryView `json:"routing_failures"`
	RefreshedAt     time.Time                 `json:"refreshed_at"`
	Window          string                    `json:"window"`
	Model           string                    `json:"model"`
}

type upstreamItem struct {
	ID                string                `json:"id"`
	Enabled           bool                  `json:"enabled"`
	Priority          int                   `json:"priority"`
	Weight            float64               `json:"weight"`
	CapacityHint      float64               `json:"capacity_hint"`
	ModelDiscovery    string                `json:"model_discovery"`
	BaseURL           string                `json:"base_url"`
	ProviderPreset    string                `json:"provider_preset"`
	ProtocolFamily    string                `json:"protocol_family"`
	RoutingProfile    string                `json:"routing_profile"`
	HealthState       string                `json:"health_state"`
	Inflight          int64                 `json:"inflight"`
	TTFTFastMs        float64               `json:"ttft_fast_ms"`
	TTFTSlowMs        float64               `json:"ttft_slow_ms"`
	LatencyFastMs     float64               `json:"latency_fast_ms"`
	ErrorRate         float64               `json:"error_rate"`
	TimeoutRate       float64               `json:"timeout_rate"`
	LastRefreshAt     time.Time             `json:"last_refresh_at"`
	LastRefreshStatus string                `json:"last_refresh_status"`
	LastRefreshError  string                `json:"last_refresh_error,omitempty"`
	OpenUntil         time.Time             `json:"open_until,omitempty"`
	Models            []string              `json:"models"`
	RequestCount      int                   `json:"request_count"`
	SuccessRequest    int                   `json:"success_request"`
	FailedRequest     int                   `json:"failed_request"`
	SuccessRate       float64               `json:"success_rate"`
	TotalTokens       int                   `json:"total_tokens"`
	AvgTTFT           int                   `json:"avg_ttft"`
	LastSeen          time.Time             `json:"last_seen"`
	RecentModels      []string              `json:"recent_models"`
	LastModel         string                `json:"last_model"`
	RecentErrors      []string              `json:"recent_errors"`
	RecentFailures    []upstreamFailureItem `json:"recent_failures"`
	Performance       performanceView       `json:"performance"`
}

type upstreamFailureItem struct {
	TraceID    string    `json:"trace_id"`
	Model      string    `json:"model"`
	Endpoint   string    `json:"endpoint"`
	Reason     string    `json:"reason"`
	StatusCode int       `json:"status_code"`
	RecordedAt time.Time `json:"recorded_at"`
	ErrorText  string    `json:"error_text,omitempty"`
}

type routingFailureSummaryView struct {
	Total    int                        `json:"total"`
	Reasons  []sessionCountItem         `json:"reasons"`
	Recent   []routingFailureItem       `json:"recent"`
	Timeline []routingFailureBucketItem `json:"timeline"`
}

type routingFailureItem struct {
	TraceID    string    `json:"trace_id"`
	Model      string    `json:"model"`
	Endpoint   string    `json:"endpoint"`
	RecordedAt time.Time `json:"recorded_at"`
	Reason     string    `json:"reason"`
	ErrorText  string    `json:"error_text,omitempty"`
	StatusCode int       `json:"status_code"`
}

type routingFailureBucketItem struct {
	Time  time.Time `json:"time"`
	Count int       `json:"count"`
}

type upstreamDetailResponse struct {
	Target           upstreamItem               `json:"target"`
	Breakdown        upstreamBreakdownView      `json:"breakdown"`
	Timeline         []upstreamFailureItem      `json:"timeline"`
	FailureTimeline  []routingFailureBucketItem `json:"failure_timeline"`
	HealthThresholds healthThresholdView        `json:"health_thresholds"`
	Traces           []traceListItem            `json:"traces"`
	RefreshedAt      time.Time                  `json:"refreshed_at"`
	Window           string                     `json:"window"`
	Model            string                     `json:"model"`
}

type healthThresholdView struct {
	TTFTDegradedRatio   float64 `json:"ttft_degraded_ratio"`
	ErrorRateDegraded   float64 `json:"error_rate_degraded"`
	TimeoutRateDegraded float64 `json:"timeout_rate_degraded"`
	ErrorRateOpen       float64 `json:"error_rate_open"`
	TimeoutRateOpen     float64 `json:"timeout_rate_open"`
	FailureThreshold    int64   `json:"failure_threshold"`
	OpenWindow          string  `json:"open_window"`
}

type upstreamBreakdownView struct {
	Models         []sessionCountItem `json:"models"`
	Endpoints      []sessionCountItem `json:"endpoints"`
	FailureReasons []sessionCountItem `json:"failure_reasons"`
	FailedTraces   int                `json:"failed_traces"`
}

type channelListResponse struct {
	Items       []channelItem `json:"items"`
	RefreshedAt time.Time     `json:"refreshed_at"`
}

type channelItem struct {
	ID                 string                `json:"id"`
	Name               string                `json:"name"`
	Description        string                `json:"description,omitempty"`
	Source             string                `json:"source"`
	BaseURL            string                `json:"base_url"`
	ProviderPreset     string                `json:"provider_preset"`
	ProtocolFamily     string                `json:"protocol_family"`
	RoutingProfile     string                `json:"routing_profile"`
	APIVersion         string                `json:"api_version,omitempty"`
	Deployment         string                `json:"deployment,omitempty"`
	Project            string                `json:"project,omitempty"`
	Location           string                `json:"location,omitempty"`
	ModelResource      string                `json:"model_resource,omitempty"`
	APIKeyHint         string                `json:"api_key_hint,omitempty"`
	SecretStorageMode  string                `json:"secret_storage_mode"`
	Headers            map[string]string     `json:"headers,omitempty"`
	Enabled            bool                  `json:"enabled"`
	Priority           int                   `json:"priority"`
	Weight             float64               `json:"weight"`
	CapacityHint       float64               `json:"capacity_hint"`
	ModelDiscovery     string                `json:"model_discovery"`
	AllowUnknownModels bool                  `json:"allow_unknown_models"`
	ModelCount         int                   `json:"model_count"`
	EnabledModelCount  int                   `json:"enabled_model_count"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
	LastProbeAt        time.Time             `json:"last_probe_at,omitempty"`
	LastProbeStatus    string                `json:"last_probe_status,omitempty"`
	LastProbeError     string                `json:"last_probe_error,omitempty"`
	Summary            usageSummaryView      `json:"summary,omitempty"`
	Trends             []usageTrendView      `json:"trends,omitempty"`
	ModelsUsage        []modelChannelItem    `json:"models_usage,omitempty"`
	RecentFailures     []upstreamFailureItem `json:"recent_failures,omitempty"`
	RecentProbeRuns    []channelProbeRunItem `json:"recent_probe_runs,omitempty"`
}

type channelProbeRunItem struct {
	ID              string    `json:"id"`
	Status          string    `json:"status"`
	FailureReason   string    `json:"failure_reason,omitempty"`
	RetryHint       string    `json:"retry_hint,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
	DurationMs      int64     `json:"duration_ms"`
	DiscoveredCount int       `json:"discovered_count"`
	EnabledCount    int       `json:"enabled_count"`
	Endpoint        string    `json:"endpoint,omitempty"`
	StatusCode      int       `json:"status_code,omitempty"`
	ErrorText       string    `json:"error_text,omitempty"`
}

type channelModelsResponse struct {
	Items       []channelModelItem `json:"items"`
	RefreshedAt time.Time          `json:"refreshed_at"`
}

type channelModelItem struct {
	Model       string    `json:"model"`
	DisplayName string    `json:"display_name,omitempty"`
	Source      string    `json:"source"`
	Enabled     bool      `json:"enabled"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	LastProbeAt time.Time `json:"last_probe_at,omitempty"`
}

type channelProbeResponse struct {
	ChannelID       string    `json:"channel_id"`
	Status          string    `json:"status"`
	FailureReason   string    `json:"failure_reason,omitempty"`
	RetryHint       string    `json:"retry_hint,omitempty"`
	Models          []string  `json:"models"`
	DiscoveredCount int       `json:"discovered_count"`
	EnabledCount    int       `json:"enabled_count"`
	Endpoint        string    `json:"endpoint,omitempty"`
	ErrorText       string    `json:"error_text,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at"`
	DurationMs      int64     `json:"duration_ms"`
}

type channelProbeRequest struct {
	EnableDiscovered *bool `json:"enable_discovered"`
}

type channelUpsertRequest struct {
	ID                 string                         `json:"id"`
	Name               string                         `json:"name"`
	Description        string                         `json:"description"`
	BaseURL            string                         `json:"base_url"`
	ProviderPreset     string                         `json:"provider_preset"`
	ProtocolFamily     string                         `json:"protocol_family"`
	RoutingProfile     string                         `json:"routing_profile"`
	APIVersion         string                         `json:"api_version"`
	Deployment         string                         `json:"deployment"`
	Project            string                         `json:"project"`
	Location           string                         `json:"location"`
	ModelResource      string                         `json:"model_resource"`
	APIKey             string                         `json:"api_key"`
	Headers            map[string]channelHeaderUpdate `json:"headers"`
	Enabled            *bool                          `json:"enabled"`
	Priority           *int                           `json:"priority"`
	Weight             *float64                       `json:"weight"`
	CapacityHint       *float64                       `json:"capacity_hint"`
	ModelDiscovery     string                         `json:"model_discovery"`
	AllowUnknownModels *bool                          `json:"allow_unknown_models"`
}

type channelHeaderUpdate struct {
	Value  string
	Keep   bool
	Delete bool
}

func (u *channelHeaderUpdate) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		u.Value = value
		return nil
	}
	var object struct {
		Value  string `json:"value"`
		Keep   bool   `json:"keep"`
		Delete bool   `json:"delete"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	u.Value = object.Value
	u.Keep = object.Keep
	u.Delete = object.Delete
	return nil
}

type channelModelPatchRequest struct {
	Enabled bool `json:"enabled"`
}

type channelModelBatchPatchRequest struct {
	Models  []string `json:"models"`
	Enabled bool     `json:"enabled"`
}

type channelModelBatchPatchResponse struct {
	Updated int      `json:"updated"`
	Models  []string `json:"models"`
	Enabled bool     `json:"enabled"`
}

type channelModelCreateRequest struct {
	Model       string `json:"model"`
	DisplayName string `json:"display_name"`
	Enabled     *bool  `json:"enabled"`
}

type usageSummaryView struct {
	RequestCount     int       `json:"request_count"`
	SuccessRequest   int       `json:"success_request"`
	FailedRequest    int       `json:"failed_request"`
	SuccessRate      float64   `json:"success_rate"`
	MissingUsage     int       `json:"missing_usage_request"`
	TotalTokens      int       `json:"total_tokens"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	CachedTokens     int       `json:"cached_tokens"`
	AvgTTFT          int       `json:"avg_ttft"`
	AvgDurationMs    int64     `json:"avg_duration_ms"`
	LastSeen         time.Time `json:"last_seen,omitempty"`
}

type usageTrendView struct {
	Time          time.Time `json:"time"`
	RequestCount  int       `json:"request_count"`
	FailedRequest int       `json:"failed_request"`
	MissingUsage  int       `json:"missing_usage_request"`
	TotalTokens   int       `json:"total_tokens"`
	ModelCount    int       `json:"model_count"`
}

type modelListResponse struct {
	Items       []modelItem `json:"items"`
	RefreshedAt time.Time   `json:"refreshed_at"`
	Window      string      `json:"window"`
}

type modelItem struct {
	Model               string           `json:"model"`
	DisplayName         string           `json:"display_name,omitempty"`
	ProviderCount       int              `json:"provider_count"`
	ChannelCount        int              `json:"channel_count"`
	EnabledChannelCount int              `json:"enabled_channel_count"`
	Channels            []string         `json:"channels"`
	Summary             usageSummaryView `json:"summary"`
	Today               usageSummaryView `json:"today"`
}

type modelDetailResponse struct {
	Model       modelItem          `json:"model"`
	Trends      []usageTrendView   `json:"trends"`
	Channels    []modelChannelItem `json:"channels"`
	RefreshedAt time.Time          `json:"refreshed_at"`
	Window      string             `json:"window"`
}

type modelChannelItem struct {
	ChannelID string           `json:"channel_id"`
	Model     string           `json:"model"`
	Enabled   bool             `json:"enabled"`
	Source    string           `json:"source"`
	Summary   usageSummaryView `json:"summary"`
}

func RegisterRoutes(mux *http.ServeMux, st *store.Store, opts ...RouteOptions) {
	var opt RouteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	mux.HandleFunc("/api/auth/status", authStatusAPIHandler(opt.AuthVerifier))
	mux.HandleFunc("/api/auth/login", authLoginAPIHandler(opt.AuthStore, opt.SessionTTL))
	mux.HandleFunc("/api/auth/check", monitorAuthRequired(authCheckAPIHandler(), opt.AuthVerifier))
	mux.HandleFunc("/api/auth/me", monitorAuthRequired(authMeAPIHandler(), opt.AuthVerifier))
	mux.HandleFunc("/api/auth/password", monitorAuthRequired(authChangePasswordAPIHandler(opt.AuthStore), opt.AuthVerifier))
	mux.HandleFunc("/api/auth/tokens", monitorAuthRequired(authTokensAPIHandler(opt.AuthStore), opt.AuthVerifier))
	mux.HandleFunc("/api/auth/tokens/", monitorAuthRequired(authTokenDetailAPIHandler(opt.AuthStore), opt.AuthVerifier))
	mux.HandleFunc("/api/overview", monitorAuthRequired(overviewAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/events/summary", monitorAuthRequired(systemEventSummaryAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/events/read-all", monitorAuthRequired(systemEventReadAllAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/events/stream", monitorAuthRequired(systemEventStreamAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/events", monitorAuthRequired(systemEventListAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/events/", monitorAuthRequired(systemEventDetailAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/traces", monitorAuthRequired(listAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/traces/", monitorAuthRequired(traceAPIHandler(st, opt.Router), opt.AuthVerifier))
	mux.HandleFunc("/api/sessions", monitorAuthRequired(sessionListAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/sessions/", monitorAuthRequired(sessionDetailAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/findings", monitorAuthRequired(findingListAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/analysis/batch/reanalyze", monitorAuthRequired(analysisBatchReanalyzeAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/analysis/jobs", monitorAuthRequired(analysisJobListAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/analysis/jobs/", monitorAuthRequired(analysisJobDetailAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/analysis", monitorAuthRequired(analysisListAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/models", monitorAuthRequired(modelListAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/models/", monitorAuthRequired(modelDetailAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/secrets/local-key", monitorAuthRequired(localSecretKeyAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/channels", monitorAuthRequired(channelListCreateAPIHandler(st, opt.Router, opt.ChannelService), opt.AuthVerifier))
	mux.HandleFunc("/api/channels/", monitorAuthRequired(channelDetailAPIHandler(st, opt.Router, opt.ChannelService), opt.AuthVerifier))
	mux.HandleFunc("/api/provider-presets", monitorAuthRequired(providerPresetAPIHandler(), opt.AuthVerifier))
	mux.HandleFunc("/api/router/reload", monitorAuthRequired(routerReloadAPIHandler(st, opt.Router, opt.ChannelService), opt.AuthVerifier))
	mux.HandleFunc("/api/upstreams", monitorAuthRequired(upstreamListAPIHandler(st, opt.Router), opt.AuthVerifier))
	mux.HandleFunc("/api/upstreams/", monitorAuthRequired(upstreamDetailAPIHandler(st, opt.Router), opt.AuthVerifier))
	mux.Handle("/", appHandler())
}

func authStatusAPIHandler(verifier auth.TokenVerifier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"auth_required": verifier != nil})
	}
}

func authLoginAPIHandler(authStore *auth.Store, ttl time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if authStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auth store not configured"})
			return
		}
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid login payload"})
			return
		}
		token, err := authStore.Login(r.Context(), req.Username, req.Password, ttl)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
			return
		}
		writeJSON(w, http.StatusOK, loginResponse{Token: token.Token, Prefix: token.Prefix})
	}
}

func authCheckAPIHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func systemEventListAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		windowLabel, since := parseSystemEventWindow(r.URL.Query().Get("window"))
		filter := systemEventFilterFromRequest(r, since)
		page, err := st.ListSystemEvents(filter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query error: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, systemEventListResponse{
			Items:       systemEventViews(page.Items),
			Page:        page.Page,
			PageSize:    page.PageSize,
			Total:       page.Total,
			TotalPages:  page.TotalPages,
			Window:      windowLabel,
			RefreshedAt: time.Now().UTC(),
		})
	}
}

func systemEventSummaryAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		windowLabel, since := parseSystemEventWindow(r.URL.Query().Get("window"))
		summary, err := st.SystemEventSummary(since)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "summary error: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, systemEventSummaryView(summary, windowLabel))
	}
}

func systemEventReadAllAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_, since := parseSystemEventWindow(r.URL.Query().Get("window"))
		filter := systemEventFilterFromRequest(r, since)
		if strings.TrimSpace(filter.Status) == "" {
			filter.Status = store.SystemEventStatusUnread
		}
		count, err := st.MarkAllSystemEventsRead(filter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mark read error: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"updated": count})
	}
}

func systemEventStreamAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ch, unsubscribe := st.SubscribeSystemEvents(16)
		defer unsubscribe()

		writeSystemEventStreamMessage(w, "system_event.summary", store.SystemEventNotification{}, st)
		flusher.Flush()

		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case notification, ok := <-ch:
				if !ok {
					return
				}
				writeSystemEventStreamMessage(w, "system_event.updated", notification, st)
				flusher.Flush()
			case <-heartbeat.C:
				_, _ = w.Write([]byte(": ping\n\n"))
				flusher.Flush()
			}
		}
	}
}

func systemEventDetailAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}
		rest := strings.Trim(strings.TrimPrefix(pathClean(r.URL.Path), "/api/events/"), "/")
		parts := strings.Split(rest, "/")
		if len(parts) != 2 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		eventID := parts[0]
		action := parts[1]
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var err error
		switch action {
		case "read":
			err = st.MarkSystemEventRead(eventID)
		case "resolve":
			err = st.ResolveSystemEvent(eventID)
		case "ignore":
			err = st.IgnoreSystemEvent(eventID)
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update error: " + err.Error()})
			return
		}
		event, err := st.GetSystemEvent(eventID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "event not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query error: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, systemEventViewFromStore(event))
	}
}

func authMeAPIHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusOK, meResponse{
			Username: principal.Username,
			Role:     principal.Role,
			Scope:    principal.Scope,
		})
	}
}

func authChangePasswordAPIHandler(authStore *auth.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if authStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auth store not configured"})
			return
		}
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok || strings.TrimSpace(principal.Username) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var req changePasswordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid password payload"})
			return
		}
		if err := authStore.VerifyPassword(r.Context(), principal.Username, req.CurrentPassword); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "current password is incorrect"})
			return
		}
		if err := authStore.ResetPassword(r.Context(), principal.Username, req.NewPassword); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func authTokensAPIHandler(authStore *auth.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleListAuthTokens(w, r, authStore)
		case http.MethodPost:
			handleCreateAuthToken(w, r, authStore)
		default:
			http.NotFound(w, r)
		}
	}
}

func handleListAuthTokens(w http.ResponseWriter, r *http.Request, authStore *auth.Store) {
	if authStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auth store not configured"})
		return
	}
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || strings.TrimSpace(principal.Username) == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	tokens, err := authStore.ListTokens(r.Context(), principal.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	items := make([]tokenItem, 0, len(tokens))
	for _, token := range tokens {
		items = append(items, tokenItemFromRecord(token))
	}
	writeJSON(w, http.StatusOK, tokenListResponse{
		Items: items,
		Total: len(items),
	})
}

func handleCreateAuthToken(w http.ResponseWriter, r *http.Request, authStore *auth.Store) {
	if authStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auth store not configured"})
		return
	}
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok || strings.TrimSpace(principal.Username) == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid token payload"})
		return
	}
	var ttl time.Duration
	if strings.TrimSpace(req.TTL) != "" {
		parsed, err := time.ParseDuration(req.TTL)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid ttl"})
			return
		}
		ttl = parsed
	}
	token, err := authStore.CreateToken(r.Context(), principal.Username, req.Name, req.Scope, ttl)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, createTokenResponse{Token: token.Token, Prefix: token.Prefix})
}

func authTokenDetailAPIHandler(authStore *auth.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		if authStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auth store not configured"})
			return
		}
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok || strings.TrimSpace(principal.Username) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		tokenIDText := strings.TrimPrefix(pathClean(r.URL.Path), "/api/auth/tokens/")
		tokenID, err := strconv.Atoi(tokenIDText)
		if err != nil || tokenID <= 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
			return
		}
		if err := authStore.RevokeToken(r.Context(), principal.Username, tokenID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "token not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func modelListAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}
		windowLabel, since := parseAnalyticsWindow(r.URL.Query().Get("window"))
		todaySince := startOfUTCDay(time.Now().UTC())
		items, err := st.ListModelCatalogAnalytics(since, todaySince)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
		resp := modelListResponse{RefreshedAt: time.Now().UTC(), Window: windowLabel}
		for _, item := range items {
			if item.Summary.RequestCount == 0 {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(item.Model), q) && !strings.Contains(strings.ToLower(item.DisplayName), q) {
				continue
			}
			resp.Items = append(resp.Items, modelItemFromRecord(item))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func modelDetailAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}
		model := strings.Trim(strings.TrimPrefix(pathClean(r.URL.Path), "/api/models/"), "/")
		if model == "" || strings.Contains(model, "/") {
			http.NotFound(w, r)
			return
		}
		windowLabel, since := parseAnalyticsWindow(r.URL.Query().Get("window"))
		bucketSize, bucketCount := analyticsBucketSpec(windowLabel)
		detail, err := st.GetModelDetailAnalytics(model, since, startOfUTCDay(time.Now().UTC()), bucketSize, bucketCount)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "model not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		resp := modelDetailResponse{
			Model:       modelItemFromRecord(detail.Model),
			Trends:      usageTrendViews(detail.Trends),
			RefreshedAt: time.Now().UTC(),
			Window:      windowLabel,
		}
		for _, channelRecord := range detail.Channels {
			resp.Channels = append(resp.Channels, modelChannelItem{
				ChannelID: channelRecord.ChannelID,
				Model:     channelRecord.Model,
				Enabled:   channelRecord.Enabled,
				Source:    channelRecord.Source,
				Summary:   usageSummaryViewFromRecord(channelRecord.Summary),
			})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func providerPresetAPIHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		matrix := upstream.PresetSupportMatrix()
		ids := make([]string, 0, len(matrix))
		for id := range matrix {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		items := make([]providerPresetItem, 0, len(ids))
		for _, id := range ids {
			spec := matrix[id]
			allowed := append([]string(nil), spec.AllowedProfiles...)
			sort.Strings(allowed)
			items = append(items, providerPresetItem{
				ID:              id,
				ProtocolFamily:  spec.ProtocolFamily,
				RoutingProfile:  spec.RoutingProfile,
				SupportLevel:    spec.SupportLevel,
				AllowedProfiles: allowed,
			})
		}
		writeJSON(w, http.StatusOK, providerPresetResponse{
			Items:   ids,
			Presets: items,
			Defaults: providerPresetFormDefaults{
				ProtocolFamilies: []string{
					upstream.ProtocolFamilyOpenAICompatible,
					upstream.ProtocolFamilyAnthropicMessages,
					upstream.ProtocolFamilyGoogleGenAI,
					upstream.ProtocolFamilyVertexNative,
				},
				RoutingProfiles: []string{
					upstream.RoutingProfileOpenAIDefault,
					upstream.RoutingProfileAzureOpenAIV1,
					upstream.RoutingProfileAzureOpenAIDeploy,
					upstream.RoutingProfileVLLMOpenAI,
					upstream.RoutingProfileAnthropicDefault,
					upstream.RoutingProfileGoogleAIStudio,
					upstream.RoutingProfileVertexExpress,
					upstream.RoutingProfileVertexProject,
				},
				ModelDiscovery: []string{"list_models", "disabled"},
			},
		})
	}
}

func localSecretKeyAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			if r.URL.Query().Get("export") == "1" {
				key, status, err := st.ExportLocalSecretKey()
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
				name := "trace_index.secret." + valueOrExisting(status.Fingerprint, "backup")
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(key)
				return
			}
			status := st.SecretStatus()
			writeJSON(w, http.StatusOK, status)
		case http.MethodPost:
			if r.URL.Query().Get("rotate") != "1" {
				http.NotFound(w, r)
				return
			}
			result, err := st.RotateLocalSecretKey()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, result)
		default:
			http.NotFound(w, r)
		}
	}
}

func monitorAuthRequired(next http.HandlerFunc, verifier auth.TokenVerifier) http.HandlerFunc {
	if verifier == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.VerifyRequest(r, verifier)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="llm-tracelab-monitor"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
	}
}

func appHandler() http.Handler {
	distFS, err := fs.Sub(uiFS, "ui/dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "embedded ui not available", http.StatusInternalServerError)
		})
	}

	fileServer := http.FileServer(http.FS(distFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		clean := strings.TrimPrefix(pathClean(r.URL.Path), "/")
		if clean == "" {
			serveEmbeddedIndex(distFS, w, r)
			return
		}
		if _, err := fs.Stat(distFS, clean); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		serveEmbeddedIndex(distFS, w, r)
	})
}

func channelListCreateAPIHandler(st *store.Store, rtr *router.Router, channelService *channel.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			windowLabel, since := parseAnalyticsWindow(r.URL.Query().Get("window"))
			bucketSize, bucketCount := analyticsBucketSpec(windowLabel)
			channels, err := st.ListChannelConfigs()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			models, err := st.ListChannelModels("", false)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			counts := channelModelCounts(models)
			items := make([]channelItem, 0, len(channels))
			for _, record := range channels {
				item := channelItemFromRecord(st, record, counts[record.ID], enabledChannelModelCount(models, record.ID))
				enrichChannelItemAnalytics(st, &item, record.ID, since, bucketSize, bucketCount, false)
				items = append(items, item)
			}
			writeJSON(w, http.StatusOK, channelListResponse{Items: items, RefreshedAt: time.Now().UTC()})
		case http.MethodPost:
			var req channelUpsertRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid channel payload"})
				return
			}
			record, err := st.UpsertChannelConfig(channelRecordFromRequest(req, store.ChannelConfigRecord{}))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if err := reloadRouterFromChannels(rtr, effectiveChannelService(st, channelService)); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload router: " + err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, channelItemFromRecord(st, record, 0, 0))
		default:
			http.NotFound(w, r)
		}
	}
}

func channelDetailAPIHandler(st *store.Store, rtr *router.Router, channelService *channel.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}
		rest := strings.TrimPrefix(pathClean(r.URL.Path), "/api/channels/")
		parts := strings.Split(strings.Trim(rest, "/"), "/")
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			http.NotFound(w, r)
			return
		}
		channelID := parts[0]
		if len(parts) == 1 {
			handleChannelConfig(w, r, st, rtr, channelService, channelID)
			return
		}
		switch parts[1] {
		case "probe":
			if r.Method != http.MethodPost {
				http.NotFound(w, r)
				return
			}
			req, err := decodeChannelProbeRequest(r)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			svc := effectiveChannelService(st, channelService)
			result, err := svc.ProbeWithOptions(channelID, channel.ProbeOptions{EnableDiscovered: req.EnableDiscovered})
			if err != nil && result.Status == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			status := http.StatusOK
			if err != nil {
				status = http.StatusBadGateway
			}
			if err == nil {
				if reloadErr := reloadRouterFromChannels(rtr, svc); reloadErr != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload router: " + reloadErr.Error()})
					return
				}
			}
			writeJSON(w, status, channelProbeResponseFromResult(result))
		case "models":
			if len(parts) == 2 {
				handleChannelModels(w, r, st, rtr, channelService, channelID)
				return
			}
			if len(parts) == 3 {
				if parts[2] == "batch" {
					handleChannelModelsBatch(w, r, st, rtr, channelService, channelID)
					return
				}
				handleChannelModel(w, r, st, rtr, channelService, channelID, parts[2])
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}
}

func decodeChannelProbeRequest(r *http.Request) (channelProbeRequest, error) {
	var req channelProbeRequest
	if r == nil || r.Body == nil || r.Body == http.NoBody {
		return req, nil
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return channelProbeRequest{}, fmt.Errorf("invalid probe payload")
	}
	return req, nil
}

func handleChannelConfig(w http.ResponseWriter, r *http.Request, st *store.Store, rtr *router.Router, channelService *channel.Service, channelID string) {
	switch r.Method {
	case http.MethodGet:
		record, err := st.GetChannelConfig(channelID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		models, err := st.ListChannelModels(channelID, false)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		item := channelItemFromRecord(st, record, len(models), enabledChannelModelCount(models, channelID))
		windowLabel, since := parseAnalyticsWindow(r.URL.Query().Get("window"))
		bucketSize, bucketCount := analyticsBucketSpec(windowLabel)
		enrichChannelItemAnalytics(st, &item, channelID, since, bucketSize, bucketCount, true)
		writeJSON(w, http.StatusOK, item)
	case http.MethodPatch:
		existing, err := st.GetChannelConfig(channelID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		var req channelUpsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid channel payload"})
			return
		}
		req.ID = channelID
		record, err := st.UpsertChannelConfig(channelRecordFromRequest(req, existing))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := reloadRouterFromChannels(rtr, effectiveChannelService(st, channelService)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload router: " + err.Error()})
			return
		}
		models, _ := st.ListChannelModels(channelID, false)
		writeJSON(w, http.StatusOK, channelItemFromRecord(st, record, len(models), enabledChannelModelCount(models, channelID)))
	default:
		http.NotFound(w, r)
	}
}

func handleChannelModels(w http.ResponseWriter, r *http.Request, st *store.Store, rtr *router.Router, channelService *channel.Service, channelID string) {
	switch r.Method {
	case http.MethodGet:
		models, err := st.ListChannelModels(channelID, false)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		items := make([]channelModelItem, 0, len(models))
		for _, model := range models {
			items = append(items, channelModelItemFromRecord(model))
		}
		writeJSON(w, http.StatusOK, channelModelsResponse{Items: items, RefreshedAt: time.Now().UTC()})
	case http.MethodPost:
		var req channelModelCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid model payload"})
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		record, err := st.UpsertChannelModel(channelID, store.ChannelModelRecord{
			Model:       req.Model,
			DisplayName: req.DisplayName,
			Source:      "manual",
			Enabled:     enabled,
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := reloadRouterFromChannels(rtr, effectiveChannelService(st, channelService)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload router: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, channelModelItemFromRecord(record))
	default:
		http.NotFound(w, r)
	}
}

func handleChannelModelsBatch(w http.ResponseWriter, r *http.Request, st *store.Store, rtr *router.Router, channelService *channel.Service, channelID string) {
	if r.Method != http.MethodPatch {
		http.NotFound(w, r)
		return
	}
	var req channelModelBatchPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid model payload"})
		return
	}
	models := normalizeModelList(req.Models)
	if len(models) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "models are required"})
		return
	}
	for _, model := range models {
		if err := st.SetChannelModelEnabled(channelID, model, req.Enabled); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if err := reloadRouterFromChannels(rtr, effectiveChannelService(st, channelService)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload router: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, channelModelBatchPatchResponse{Updated: len(models), Models: models, Enabled: req.Enabled})
}

func handleChannelModel(w http.ResponseWriter, r *http.Request, st *store.Store, rtr *router.Router, channelService *channel.Service, channelID string, model string) {
	if r.Method != http.MethodPatch {
		http.NotFound(w, r)
		return
	}
	var req channelModelPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid model payload"})
		return
	}
	if err := st.SetChannelModelEnabled(channelID, model, req.Enabled); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := reloadRouterFromChannels(rtr, effectiveChannelService(st, channelService)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload router: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func normalizeModelList(models []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.ToLower(strings.TrimSpace(model))
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

func routerReloadAPIHandler(st *store.Store, rtr *router.Router, channelService *channel.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := reloadRouterFromChannels(rtr, effectiveChannelService(st, channelService)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func reloadRouterFromChannels(rtr *router.Router, channelService *channel.Service) error {
	if rtr == nil {
		return nil
	}
	if channelService == nil {
		return fmt.Errorf("channel service not configured")
	}
	targets, err := channelService.RuntimeTargets()
	if err != nil {
		return err
	}
	return rtr.Reload(targets)
}

func effectiveChannelService(st *store.Store, channelService *channel.Service) *channel.Service {
	if channelService != nil {
		return channelService
	}
	if st == nil {
		return nil
	}
	return channel.NewService(st)
}

func channelRecordFromRequest(req channelUpsertRequest, existing store.ChannelConfigRecord) store.ChannelConfigRecord {
	record := existing
	if strings.TrimSpace(req.ID) != "" {
		record.ID = strings.TrimSpace(req.ID)
	}
	if strings.TrimSpace(req.Name) != "" {
		record.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.Description) != "" {
		record.Description = strings.TrimSpace(req.Description)
	}
	if strings.TrimSpace(record.Source) == "" {
		record.Source = "manual"
	}
	if strings.TrimSpace(req.BaseURL) != "" {
		record.BaseURL = strings.TrimSpace(req.BaseURL)
	}
	if strings.TrimSpace(req.ProviderPreset) != "" {
		record.ProviderPreset = strings.TrimSpace(req.ProviderPreset)
	}
	if strings.TrimSpace(req.ProtocolFamily) != "" {
		record.ProtocolFamily = strings.TrimSpace(req.ProtocolFamily)
	}
	if strings.TrimSpace(req.RoutingProfile) != "" {
		record.RoutingProfile = strings.TrimSpace(req.RoutingProfile)
	}
	record.APIVersion = valueOrExisting(req.APIVersion, record.APIVersion)
	record.Deployment = valueOrExisting(req.Deployment, record.Deployment)
	record.Project = valueOrExisting(req.Project, record.Project)
	record.Location = valueOrExisting(req.Location, record.Location)
	record.ModelResource = valueOrExisting(req.ModelResource, record.ModelResource)
	if strings.TrimSpace(req.APIKey) != "" {
		record.APIKeyCiphertext = []byte(strings.TrimSpace(req.APIKey))
		record.APIKeyHint = secretHint(req.APIKey)
	}
	if req.Headers != nil {
		headers := applyHeaderUpdates(existing.HeadersJSON, req.Headers)
		data, _ := json.Marshal(headers)
		record.HeadersJSON = string(data)
	}
	if req.Enabled != nil {
		record.Enabled = *req.Enabled
	} else if record.ID == "" {
		record.Enabled = true
	}
	if req.Priority != nil {
		record.Priority = *req.Priority
	}
	if req.Weight != nil {
		record.Weight = *req.Weight
	}
	if req.CapacityHint != nil {
		record.CapacityHint = *req.CapacityHint
	}
	if strings.TrimSpace(req.ModelDiscovery) != "" {
		record.ModelDiscovery = strings.TrimSpace(req.ModelDiscovery)
	}
	if req.AllowUnknownModels != nil {
		record.AllowUnknownModels = *req.AllowUnknownModels
	}
	return record
}

func channelItemFromRecord(st *store.Store, record store.ChannelConfigRecord, modelCount int, enabledModelCount int) channelItem {
	headers := map[string]string{}
	if strings.TrimSpace(record.HeadersJSON) != "" {
		_ = json.Unmarshal([]byte(record.HeadersJSON), &headers)
	}
	return channelItem{
		ID:                 record.ID,
		Name:               record.Name,
		Description:        record.Description,
		Source:             record.Source,
		BaseURL:            record.BaseURL,
		ProviderPreset:     record.ProviderPreset,
		ProtocolFamily:     record.ProtocolFamily,
		RoutingProfile:     record.RoutingProfile,
		APIVersion:         record.APIVersion,
		Deployment:         record.Deployment,
		Project:            record.Project,
		Location:           record.Location,
		ModelResource:      record.ModelResource,
		APIKeyHint:         record.APIKeyHint,
		SecretStorageMode:  st.SecretStorageMode(),
		Headers:            redactHeaders(headers),
		Enabled:            record.Enabled,
		Priority:           record.Priority,
		Weight:             record.Weight,
		CapacityHint:       record.CapacityHint,
		ModelDiscovery:     record.ModelDiscovery,
		AllowUnknownModels: record.AllowUnknownModels,
		ModelCount:         modelCount,
		EnabledModelCount:  enabledModelCount,
		CreatedAt:          record.CreatedAt,
		UpdatedAt:          record.UpdatedAt,
		LastProbeAt:        record.LastProbeAt,
		LastProbeStatus:    record.LastProbeStatus,
		LastProbeError:     record.LastProbeError,
	}
}

func channelModelItemFromRecord(record store.ChannelModelRecord) channelModelItem {
	return channelModelItem{
		Model:       record.Model,
		DisplayName: record.DisplayName,
		Source:      record.Source,
		Enabled:     record.Enabled,
		FirstSeenAt: record.FirstSeenAt,
		LastSeenAt:  record.LastSeenAt,
		LastProbeAt: record.LastProbeAt,
	}
}

func enrichChannelItemAnalytics(st *store.Store, item *channelItem, channelID string, since time.Time, bucketSize time.Duration, bucketCount int, includeDetail bool) {
	if st == nil || item == nil {
		return
	}
	if summary, err := st.GetChannelUsageSummary(channelID, since); err == nil {
		item.Summary = usageSummaryViewFromRecord(summary)
	}
	if trends, err := st.GetChannelUsageTrends(channelID, since, bucketSize, bucketCount); err == nil {
		item.Trends = usageTrendViews(trends)
	}
	if !includeDetail {
		return
	}
	if models, err := st.GetChannelModelUsage(channelID, since); err == nil {
		item.ModelsUsage = make([]modelChannelItem, 0, len(models))
		for _, model := range models {
			item.ModelsUsage = append(item.ModelsUsage, modelChannelItem{
				ChannelID: model.ChannelID,
				Model:     model.Model,
				Enabled:   model.Enabled,
				Source:    model.Source,
				Summary:   usageSummaryViewFromRecord(model.Summary),
			})
		}
	}
	if failures, err := st.GetChannelRecentFailures(channelID, since, 10); err == nil {
		item.RecentFailures = toUpstreamFailureItems(failures)
	}
	if probeRuns, err := st.ListChannelProbeRuns(channelID, 8); err == nil {
		item.RecentProbeRuns = channelProbeRunItems(probeRuns)
	}
}

func channelProbeResponseFromResult(result channel.ProbeResult) channelProbeResponse {
	return channelProbeResponse{
		ChannelID:       result.ChannelID,
		Status:          result.Status,
		FailureReason:   result.FailureReason,
		RetryHint:       result.RetryHint,
		Models:          result.Models,
		DiscoveredCount: result.DiscoveredCount,
		EnabledCount:    result.EnabledCount,
		Endpoint:        result.Endpoint,
		ErrorText:       result.ErrorText,
		StartedAt:       result.StartedAt,
		CompletedAt:     result.CompletedAt,
		DurationMs:      result.DurationMs,
	}
}

func channelProbeRunItems(records []store.ChannelProbeRunRecord) []channelProbeRunItem {
	items := make([]channelProbeRunItem, 0, len(records))
	for _, record := range records {
		reason, hint := probeRunMeta(record.RequestMetaJSON)
		items = append(items, channelProbeRunItem{
			ID:              record.ID,
			Status:          record.Status,
			FailureReason:   reason,
			RetryHint:       hint,
			StartedAt:       record.StartedAt,
			CompletedAt:     record.CompletedAt,
			DurationMs:      record.DurationMs,
			DiscoveredCount: record.DiscoveredCount,
			EnabledCount:    record.EnabledCount,
			Endpoint:        record.Endpoint,
			StatusCode:      record.StatusCode,
			ErrorText:       record.ErrorText,
		})
	}
	return items
}

func probeRunMeta(raw string) (string, string) {
	var meta struct {
		FailureReason string `json:"failure_reason"`
		RetryHint     string `json:"retry_hint"`
	}
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return "", ""
	}
	return meta.FailureReason, meta.RetryHint
}

func modelItemFromRecord(record store.ModelCatalogAnalyticsRecord) modelItem {
	return modelItem{
		Model:               record.Model,
		DisplayName:         record.DisplayName,
		ProviderCount:       record.ProviderCount,
		ChannelCount:        record.ChannelCount,
		EnabledChannelCount: record.EnabledChannelCount,
		Channels:            record.Channels,
		Summary:             usageSummaryViewFromRecord(record.Summary),
		Today:               usageSummaryViewFromRecord(record.Today),
	}
}

func usageSummaryViewFromRecord(record store.UsageSummaryRecord) usageSummaryView {
	return usageSummaryView{
		RequestCount:     record.RequestCount,
		SuccessRequest:   record.SuccessRequest,
		FailedRequest:    record.FailedRequest,
		SuccessRate:      record.SuccessRate,
		MissingUsage:     record.MissingUsage,
		TotalTokens:      record.TotalTokens,
		PromptTokens:     record.PromptTokens,
		CompletionTokens: record.CompletionTokens,
		CachedTokens:     record.CachedTokens,
		AvgTTFT:          record.AvgTTFT,
		AvgDurationMs:    record.AvgDurationMs,
		LastSeen:         record.LastSeen,
	}
}

func usageTrendViews(records []store.UsageTrendRecord) []usageTrendView {
	out := make([]usageTrendView, 0, len(records))
	for _, record := range records {
		out = append(out, usageTrendView{
			Time:          record.Time,
			RequestCount:  record.RequestCount,
			FailedRequest: record.FailedRequest,
			MissingUsage:  record.MissingUsage,
			TotalTokens:   record.TotalTokens,
			ModelCount:    record.ModelCount,
		})
	}
	return out
}

func channelModelCounts(models []store.ChannelModelRecord) map[string]int {
	counts := map[string]int{}
	for _, model := range models {
		counts[model.ChannelID]++
	}
	return counts
}

func enabledChannelModelCount(models []store.ChannelModelRecord, channelID string) int {
	count := 0
	for _, model := range models {
		if model.ChannelID == channelID && model.Enabled {
			count++
		}
	}
	return count
}

func redactHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		if isSecretHeader(key) && strings.TrimSpace(value) != "" {
			out[key] = "***"
			continue
		}
		out[key] = value
	}
	return out
}

func applyHeaderUpdates(existingJSON string, updates map[string]channelHeaderUpdate) map[string]string {
	out := map[string]string{}
	if strings.TrimSpace(existingJSON) != "" {
		_ = json.Unmarshal([]byte(existingJSON), &out)
	}
	next := map[string]string{}
	for key, update := range updates {
		name := strings.TrimSpace(key)
		if name == "" || update.Delete {
			continue
		}
		if update.Keep {
			if value, ok := out[name]; ok {
				next[name] = value
			}
			continue
		}
		next[name] = update.Value
	}
	return next
}

func isSecretHeader(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "authorization" || strings.Contains(key, "api-key") || strings.Contains(key, "apikey") || strings.Contains(key, "token")
}

func valueOrExisting(value string, existing string) string {
	if strings.TrimSpace(value) == "" {
		return existing
	}
	return strings.TrimSpace(value)
}

func secretHint(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return secret
	}
	return secret[:3] + "..." + secret[len(secret)-4:]
}

func upstreamListAPIHandler(st *store.Store, rtr *router.Router) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		windowLabel, since := parseUpstreamWindow(r.URL.Query().Get("window"))
		modelFilter := strings.TrimSpace(r.URL.Query().Get("model"))
		items, err := buildUpstreamItems(st, rtr, since, modelFilter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		routingFailures := routingFailureSummaryView{}
		if st != nil {
			bucketSize, bucketCount := routingFailureBucketSpec(windowLabel)
			analytics, err := st.GetRoutingFailureAnalytics(since, modelFilter, 5, 5, bucketSize, bucketCount)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query routing failures: " + err.Error()})
				return
			}
			routingFailures = routingFailureSummaryView{
				Total:    analytics.Total,
				Reasons:  toSessionCountItems(analytics.Reasons),
				Recent:   toRoutingFailureItems(analytics.Recent),
				Timeline: toRoutingFailureBucketItems(analytics.Timeline),
			}
		}

		writeJSON(w, http.StatusOK, upstreamListResponse{
			Items:           items,
			RoutingFailures: routingFailures,
			RefreshedAt:     time.Now().UTC(),
			Window:          windowLabel,
			Model:           modelFilter,
		})
	}
}

func upstreamDetailAPIHandler(st *store.Store, rtr *router.Router) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}

		upstreamID := strings.TrimPrefix(pathClean(r.URL.Path), "/api/upstreams/")
		upstreamID = strings.Trim(upstreamID, "/")
		if upstreamID == "" || strings.Contains(upstreamID, "/") {
			http.NotFound(w, r)
			return
		}

		windowLabel, since := parseUpstreamWindow(r.URL.Query().Get("window"))
		modelFilter := strings.TrimSpace(r.URL.Query().Get("model"))
		bucketSize, bucketCount := routingFailureBucketSpec(windowLabel)
		detail, err := st.GetUpstreamDetail(upstreamID, since, modelFilter, 50, bucketSize, bucketCount)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "upstream not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query upstream detail: " + err.Error()})
			return
		}

		items, err := buildUpstreamItems(st, rtr, since, modelFilter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		var target upstreamItem
		for _, item := range items {
			if item.ID == upstreamID {
				target = item
				break
			}
		}
		if target.ID == "" {
			baseURL := ""
			providerPreset := ""
			if len(detail.Traces) > 0 {
				baseURL = detail.Traces[0].Header.Meta.SelectedUpstreamBaseURL
				providerPreset = detail.Traces[0].Header.Meta.SelectedUpstreamProviderPreset
			}
			target = upstreamItem{
				ID:             detail.Analytics.UpstreamID,
				BaseURL:        baseURL,
				ProviderPreset: providerPreset,
				RequestCount:   detail.Analytics.RequestCount,
				SuccessRequest: detail.Analytics.SuccessRequest,
				FailedRequest:  detail.Analytics.FailedRequest,
				SuccessRate:    detail.Analytics.SuccessRate,
				TotalTokens:    detail.Analytics.TotalTokens,
				AvgTTFT:        detail.Analytics.AvgTTFT,
				LastSeen:       detail.Analytics.LastSeen,
				RecentModels:   detail.Analytics.Models,
				LastModel:      detail.Analytics.LastModel,
				RecentErrors:   detail.Analytics.RecentErrors,
				RecentFailures: toUpstreamFailureItems(detail.Analytics.RecentFailures),
			}
		}

		resp := upstreamDetailResponse{
			Target: target,
			Breakdown: upstreamBreakdownView{
				Models:         toSessionCountItems(detail.Models),
				Endpoints:      toSessionCountItems(detail.Endpoints),
				FailureReasons: toSessionCountItems(detail.FailureReasons),
				FailedTraces:   detail.Analytics.FailedRequest,
			},
			Timeline:        toUpstreamFailureItems(detail.Analytics.RecentFailures),
			FailureTimeline: toRoutingFailureBucketItems(detail.Timeline),
			HealthThresholds: toHealthThresholdView(func() router.HealthThresholds {
				if rtr != nil {
					return rtr.HealthThresholds()
				}
				return router.DefaultHealthThresholds()
			}()),
			RefreshedAt: time.Now().UTC(),
			Window:      windowLabel,
			Model:       modelFilter,
		}
		for _, entry := range detail.Traces {
			resp.Traces = append(resp.Traces, traceListItemFromEntry(entry))
		}
		resp.Target.Performance = buildUpstreamPerformance(resp.Target)
		writeJSON(w, http.StatusOK, resp)
	}
}

func buildUpstreamItems(st *store.Store, rtr *router.Router, since time.Time, modelFilter string) ([]upstreamItem, error) {
	var items []upstreamItem
	analyticsByID := map[string]store.UpstreamAnalyticsRecord{}
	if st != nil {
		analytics, err := st.ListUpstreamAnalytics(5, 3, since, modelFilter)
		if err != nil {
			return nil, fmt.Errorf("query upstream analytics: %w", err)
		}
		for _, item := range analytics {
			analyticsByID[item.UpstreamID] = item
		}
	}

	switch {
	case rtr != nil:
		for _, snapshot := range rtr.Snapshots() {
			items = append(items, newUpstreamItemFromSnapshot(snapshot, analyticsByID[snapshot.ID]))
		}
	case st != nil:
		targets, err := st.ListUpstreamTargets()
		if err != nil {
			return nil, fmt.Errorf("query upstream targets: %w", err)
		}
		models, err := st.ListUpstreamModels()
		if err != nil {
			return nil, fmt.Errorf("query upstream models: %w", err)
		}
		modelMap := make(map[string][]string)
		for _, model := range models {
			modelMap[model.UpstreamID] = append(modelMap[model.UpstreamID], model.Model)
		}
		for _, target := range targets {
			sort.Strings(modelMap[target.ID])
			items = append(items, newUpstreamItemFromStore(target, modelMap[target.ID], analyticsByID[target.ID]))
		}
	default:
		return nil, errors.New("router not configured")
	}
	return items, nil
}

func newUpstreamItemFromSnapshot(snapshot router.Snapshot, analytics store.UpstreamAnalyticsRecord) upstreamItem {
	return upstreamItem{
		ID:                snapshot.ID,
		Enabled:           snapshot.Enabled,
		Priority:          snapshot.Priority,
		Weight:            snapshot.Weight,
		CapacityHint:      snapshot.CapacityHint,
		ModelDiscovery:    snapshot.ModelDiscovery,
		BaseURL:           snapshot.BaseURL,
		ProviderPreset:    snapshot.ProviderPreset,
		ProtocolFamily:    snapshot.ProtocolFamily,
		RoutingProfile:    snapshot.RoutingProfile,
		HealthState:       snapshot.HealthState,
		Inflight:          snapshot.Inflight,
		TTFTFastMs:        snapshot.TTFTFastMs,
		TTFTSlowMs:        snapshot.TTFTSlowMs,
		LatencyFastMs:     snapshot.LatencyFastMs,
		ErrorRate:         snapshot.ErrorRate,
		TimeoutRate:       snapshot.TimeoutRate,
		LastRefreshAt:     snapshot.LastRefreshAt,
		LastRefreshStatus: snapshot.LastRefreshStatus,
		LastRefreshError:  snapshot.LastRefreshError,
		OpenUntil:         snapshot.OpenUntil,
		Models:            snapshot.Models,
		RequestCount:      analytics.RequestCount,
		SuccessRequest:    analytics.SuccessRequest,
		FailedRequest:     analytics.FailedRequest,
		SuccessRate:       analytics.SuccessRate,
		TotalTokens:       analytics.TotalTokens,
		AvgTTFT:           analytics.AvgTTFT,
		LastSeen:          analytics.LastSeen,
		RecentModels:      analytics.Models,
		LastModel:         analytics.LastModel,
		RecentErrors:      analytics.RecentErrors,
		RecentFailures:    toUpstreamFailureItems(analytics.RecentFailures),
	}
}

func toHealthThresholdView(thresholds router.HealthThresholds) healthThresholdView {
	return healthThresholdView{
		TTFTDegradedRatio:   thresholds.TTFTDegradedRatio,
		ErrorRateDegraded:   thresholds.ErrorRateDegraded,
		TimeoutRateDegraded: thresholds.TimeoutRateDegraded,
		ErrorRateOpen:       thresholds.ErrorRateOpen,
		TimeoutRateOpen:     thresholds.TimeoutRateOpen,
		FailureThreshold:    thresholds.FailureThreshold,
		OpenWindow:          thresholds.OpenWindow.String(),
	}
}

func selectedUpstreamHealthView(rtr *router.Router, upstreamID string) *traceUpstreamHealthView {
	if rtr == nil || strings.TrimSpace(upstreamID) == "" {
		return nil
	}
	thresholds := toHealthThresholdView(rtr.HealthThresholds())
	for _, snapshot := range rtr.Snapshots() {
		if snapshot.ID != upstreamID {
			continue
		}
		return &traceUpstreamHealthView{
			ID:                snapshot.ID,
			HealthState:       snapshot.HealthState,
			TTFTFastMs:        snapshot.TTFTFastMs,
			TTFTSlowMs:        snapshot.TTFTSlowMs,
			LatencyFastMs:     snapshot.LatencyFastMs,
			ErrorRate:         snapshot.ErrorRate,
			TimeoutRate:       snapshot.TimeoutRate,
			Inflight:          snapshot.Inflight,
			LastRefreshAt:     snapshot.LastRefreshAt,
			LastRefreshStatus: snapshot.LastRefreshStatus,
			HealthThresholds:  thresholds,
		}
	}
	return nil
}

func newUpstreamItemFromStore(target store.UpstreamTargetRecord, models []string, analytics store.UpstreamAnalyticsRecord) upstreamItem {
	return upstreamItem{
		ID:                target.ID,
		Enabled:           target.Enabled,
		Priority:          target.Priority,
		Weight:            target.Weight,
		CapacityHint:      target.CapacityHint,
		BaseURL:           target.BaseURL,
		ProviderPreset:    target.ProviderPreset,
		ProtocolFamily:    target.ProtocolFamily,
		RoutingProfile:    target.RoutingProfile,
		HealthState:       "unknown",
		LastRefreshAt:     target.LastRefreshAt,
		LastRefreshStatus: target.LastRefreshStatus,
		LastRefreshError:  target.LastRefreshError,
		Models:            models,
		RequestCount:      analytics.RequestCount,
		SuccessRequest:    analytics.SuccessRequest,
		FailedRequest:     analytics.FailedRequest,
		SuccessRate:       analytics.SuccessRate,
		TotalTokens:       analytics.TotalTokens,
		AvgTTFT:           analytics.AvgTTFT,
		LastSeen:          analytics.LastSeen,
		RecentModels:      analytics.Models,
		LastModel:         analytics.LastModel,
		RecentErrors:      analytics.RecentErrors,
		RecentFailures:    toUpstreamFailureItems(analytics.RecentFailures),
	}
}

func toUpstreamFailureItems(records []store.UpstreamFailureRecord) []upstreamFailureItem {
	out := make([]upstreamFailureItem, 0, len(records))
	for _, record := range records {
		out = append(out, upstreamFailureItem{
			TraceID:    record.TraceID,
			Model:      record.Model,
			Endpoint:   record.Endpoint,
			Reason:     record.Reason,
			StatusCode: record.StatusCode,
			RecordedAt: record.RecordedAt,
			ErrorText:  record.ErrorText,
		})
	}
	return out
}

func toRoutingFailureItems(records []store.RoutingFailureRecord) []routingFailureItem {
	out := make([]routingFailureItem, 0, len(records))
	for _, record := range records {
		out = append(out, routingFailureItem{
			TraceID:    record.TraceID,
			Model:      record.Model,
			Endpoint:   record.Endpoint,
			RecordedAt: record.RecordedAt,
			Reason:     record.Reason,
			ErrorText:  record.ErrorText,
			StatusCode: record.StatusCode,
		})
	}
	return out
}

func toRoutingFailureBucketItems(records []store.TimeCountItem) []routingFailureBucketItem {
	out := make([]routingFailureBucketItem, 0, len(records))
	for _, record := range records {
		out = append(out, routingFailureBucketItem{
			Time:  record.Time,
			Count: record.Count,
		})
	}
	return out
}

func routingFailureBucketSpec(window string) (time.Duration, int) {
	switch window {
	case "1h":
		return 5 * time.Minute, 12
	case "7d":
		return 12 * time.Hour, 14
	case "all":
		return 24 * time.Hour, 14
	default:
		return 2 * time.Hour, 12
	}
}

func analyticsBucketSpec(window string) (time.Duration, int) {
	switch window {
	case "30d":
		return 24 * time.Hour, 30
	case "7d":
		return 24 * time.Hour, 7
	case "all":
		return 24 * time.Hour, 30
	default:
		return time.Hour, 24
	}
}

func parseUpstreamWindow(value string) (string, time.Time) {
	now := time.Now().UTC()
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1h":
		return "1h", now.Add(-1 * time.Hour)
	case "7d":
		return "7d", now.Add(-7 * 24 * time.Hour)
	case "all":
		return "all", time.Time{}
	case "", "24h":
		return "24h", now.Add(-24 * time.Hour)
	default:
		return "24h", now.Add(-24 * time.Hour)
	}
}

func parseOverviewWindow(value string) (string, time.Time, time.Duration, int) {
	now := time.Now().UTC()
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1h":
		return "1h", now.Add(-time.Hour), 5 * time.Minute, 12
	case "7d":
		return "7d", now.Add(-7 * 24 * time.Hour), 12 * time.Hour, 14
	case "all":
		return "all", time.Time{}, 24 * time.Hour, 14
	case "", "24h":
		return "24h", now.Add(-24 * time.Hour), 2 * time.Hour, 12
	default:
		return "24h", now.Add(-24 * time.Hour), 2 * time.Hour, 12
	}
}

func parseAnalyticsWindow(value string) (string, time.Time) {
	now := time.Now().UTC()
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "7d":
		return "7d", now.Add(-7 * 24 * time.Hour)
	case "30d":
		return "30d", now.Add(-30 * 24 * time.Hour)
	case "all":
		return "all", time.Time{}
	case "", "24h":
		return "24h", now.Add(-24 * time.Hour)
	default:
		return "24h", now.Add(-24 * time.Hour)
	}
}

func parseSystemEventWindow(value string) (string, time.Time) {
	now := time.Now().UTC()
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1h":
		return "1h", now.Add(-time.Hour)
	case "7d":
		return "7d", now.Add(-7 * 24 * time.Hour)
	case "all":
		return "all", time.Time{}
	case "", "24h":
		return "24h", now.Add(-24 * time.Hour)
	default:
		return "24h", now.Add(-24 * time.Hour)
	}
}

func systemEventFilterFromRequest(r *http.Request, since time.Time) store.SystemEventFilter {
	q := r.URL.Query()
	return store.SystemEventFilter{
		Status:   q.Get("status"),
		Severity: q.Get("severity"),
		Source:   q.Get("source"),
		Category: q.Get("category"),
		Query:    q.Get("q"),
		Since:    since,
		Page:     parseInt(q.Get("page"), 1),
		PageSize: parseInt(q.Get("page_size"), 50),
	}
}

func startOfUTCDay(now time.Time) time.Time {
	year, month, day := now.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func serveEmbeddedIndex(distFS fs.FS, w http.ResponseWriter, r *http.Request) {
	content, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		http.Error(w, "embedded ui not available", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(content)
}

func listAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}

		page := parseInt(r.URL.Query().Get("page"), 1)
		pageSize := parseInt(r.URL.Query().Get("page_size"), 50)
		filter := parseListFilter(r)
		result, err := st.ListPage(page, pageSize, filter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query error: " + err.Error()})
			return
		}
		stats, err := st.Stats()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "stats error: " + err.Error()})
			return
		}

		resp := listResponse{
			Page:       result.Page,
			PageSize:   result.PageSize,
			Total:      result.Total,
			TotalPages: result.TotalPages,
			Stats: LogStats{
				TotalRequest:   stats.TotalRequest,
				AvgTTFT:        stats.AvgTTFT,
				TotalTokens:    stats.TotalTokens,
				SuccessRequest: stats.SuccessRequest,
				FailedRequest:  stats.FailedRequest,
				SuccessRate:    stats.SuccessRate,
			},
			RefreshedAt: time.Now().UTC(),
		}
		for _, entry := range result.Items {
			resp.Items = append(resp.Items, traceListItem{
				ID:               entry.ID,
				SessionID:        entry.SessionID,
				SessionSource:    entry.SessionSource,
				RecordedAt:       entry.Header.Meta.Time,
				Model:            entry.Header.Meta.Model,
				Provider:         entry.Header.Meta.Provider,
				SelectedUpstream: entry.Header.Meta.SelectedUpstreamID,
				Operation:        entry.Header.Meta.Operation,
				Endpoint:         entry.Header.Meta.Endpoint,
				Method:           entry.Header.Meta.Method,
				URL:              entry.Header.Meta.URL,
				StatusCode:       entry.Header.Meta.StatusCode,
				DurationMs:       entry.Header.Meta.DurationMs,
				TTFTMs:           entry.Header.Meta.TTFTMs,
				TotalTokens:      entry.Header.Usage.TotalTokens,
				PromptTokens:     entry.Header.Usage.PromptTokens,
				CompletionTokens: entry.Header.Usage.CompletionTokens,
				CachedTokens:     cachedTokens(entry),
				IsStream:         entry.Header.Layout.IsStream,
				Error:            entry.Header.Meta.Error,
			})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func sessionListAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}

		page := parseInt(r.URL.Query().Get("page"), 1)
		pageSize := parseInt(r.URL.Query().Get("page_size"), 50)
		filter := parseListFilter(r)
		result, err := st.ListSessionPage(page, pageSize, filter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query error: " + err.Error()})
			return
		}

		resp := sessionListResponse{
			Page:        result.Page,
			PageSize:    result.PageSize,
			Total:       result.Total,
			TotalPages:  result.TotalPages,
			RefreshedAt: time.Now().UTC(),
		}
		for _, item := range result.Items {
			resp.Items = append(resp.Items, sessionSummaryItem(item))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func sessionDetailAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}

		path := strings.TrimPrefix(pathClean(r.URL.Path), "/api/sessions/")
		path = strings.Trim(path, "/")
		parts := strings.Split(path, "/")
		sessionID := parts[0]
		if sessionID == "" || len(parts) > 2 {
			http.NotFound(w, r)
			return
		}
		if len(parts) == 2 {
			if parts[1] == "analysis" && r.Method == http.MethodGet {
				handleSessionAnalysis(w, r, st, sessionID)
				return
			}
			if parts[1] == "reanalyze" && r.Method == http.MethodPost {
				handleSessionReanalyze(w, r, st, sessionID)
				return
			}
			http.NotFound(w, r)
			return
		}

		summary, err := st.GetSession(sessionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) || errors.Is(err, os.ErrNotExist) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query error: " + err.Error()})
			return
		}
		traces, err := st.ListTracesBySession(sessionID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query error: " + err.Error()})
			return
		}

		resp := sessionDetailResponse{
			Summary: sessionSummaryItem(summary),
		}
		for _, entry := range traces {
			resp.Traces = append(resp.Traces, traceListItem{
				ID:               entry.ID,
				SessionID:        entry.SessionID,
				SessionSource:    entry.SessionSource,
				RecordedAt:       entry.Header.Meta.Time,
				Model:            entry.Header.Meta.Model,
				Provider:         entry.Header.Meta.Provider,
				Operation:        entry.Header.Meta.Operation,
				Endpoint:         entry.Header.Meta.Endpoint,
				Method:           entry.Header.Meta.Method,
				URL:              entry.Header.Meta.URL,
				StatusCode:       entry.Header.Meta.StatusCode,
				DurationMs:       entry.Header.Meta.DurationMs,
				TTFTMs:           entry.Header.Meta.TTFTMs,
				TotalTokens:      entry.Header.Usage.TotalTokens,
				PromptTokens:     entry.Header.Usage.PromptTokens,
				CompletionTokens: entry.Header.Usage.CompletionTokens,
				CachedTokens:     cachedTokens(entry),
				IsStream:         entry.Header.Layout.IsStream,
				Error:            entry.Header.Meta.Error,
			})
		}
		resp.Breakdown = buildSessionBreakdown(resp.Traces)
		resp.Timeline = buildSessionTimeline(resp.Traces)
		resp.Performance = buildAggregatePerformance(resp.Traces)
		if runs, err := st.ListAnalysisRuns(sessionID, "", "", 10); err == nil {
			resp.Analysis = analysisRunViews(runs)
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleSessionAnalysis(w http.ResponseWriter, r *http.Request, st *store.Store, sessionID string) {
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	limit := parseInt(r.URL.Query().Get("limit"), 20)
	runs, err := st.ListAnalysisRuns(sessionID, "", kind, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query analysis runs: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, analysisListResponse{
		SessionID: sessionID,
		Items:     analysisRunViews(runs),
		Total:     len(runs),
	})
}

func handleSessionReanalyze(w http.ResponseWriter, r *http.Request, st *store.Store, sessionID string) {
	req, ok := decodeReanalysisRequest(w, r)
	if !ok {
		return
	}
	opts := reanalysis.SessionOptions{
		Reparse: req.Reparse,
		Scan:    req.Scan,
	}
	mode := requestMode(req.Mode, "async")
	svc := reanalysis.New(st, reanalysis.Options{})
	if mode == "async" {
		job, err := svc.EnqueueSessionReanalyze(sessionID, opts)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, reanalysisResponse{Job: analysisJobViewFromStore(job)})
		return
	}
	if mode != "sync" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be sync or async"})
		return
	}
	result, err := svc.ReanalyzeSession(r.Context(), sessionID, opts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, reanalysisResponse{Job: analysisJobViewFromStore(result.Job), Result: result})
}

func findingListAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		filter := store.FindingFilter{
			Category: strings.TrimSpace(r.URL.Query().Get("category")),
			Severity: strings.TrimSpace(r.URL.Query().Get("severity")),
		}
		limit := parseInt(r.URL.Query().Get("limit"), 50)
		findings, err := st.ListAllFindings(filter, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		items := make([]findingView, 0, len(findings))
		for _, finding := range findings {
			items = append(items, findingViewFromObservation(finding))
		}
		writeJSON(w, http.StatusOK, findingListResponse{
			Items:    items,
			Total:    len(items),
			Severity: filter.Severity,
			Category: filter.Category,
		})
	}
}

func analysisListAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		kind := strings.TrimSpace(r.URL.Query().Get("kind"))
		limit := parseInt(r.URL.Query().Get("limit"), 50)
		runs, err := st.ListAnalysisRuns("", "", kind, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, analysisListResponse{
			Items: analysisRunViews(runs),
			Total: len(runs),
		})
	}
}

func analysisJobListAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		targetType := strings.TrimSpace(r.URL.Query().Get("target_type"))
		targetID := strings.TrimSpace(r.URL.Query().Get("target_id"))
		limit := parseInt(r.URL.Query().Get("limit"), 50)
		jobs, err := st.ListAnalysisJobs(status, targetType, targetID, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		items := make([]analysisJobView, 0, len(jobs))
		for _, job := range jobs {
			items = append(items, analysisJobViewFromStore(job))
		}
		writeJSON(w, http.StatusOK, analysisJobListResponse{Items: items, Total: len(items)})
	}
}

func analysisBatchReanalyzeAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var req batchReanalysisRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
			return
		}
		opts := reanalysis.BatchOptions{
			Filter: store.ListFilter{
				Query:            strings.TrimSpace(req.Query),
				Provider:         strings.TrimSpace(req.Provider),
				Model:            strings.TrimSpace(req.Model),
				Endpoint:         strings.TrimSpace(req.Endpoint),
				SelectedUpstream: strings.TrimSpace(req.Upstream),
				Status:           strings.TrimSpace(req.Status),
				MissingUsage:     req.MissingUsage,
			},
			Limit:       req.Limit,
			RepairUsage: req.RepairUsage,
			Reparse:     req.Reparse,
			Scan:        req.Scan,
		}
		mode := requestMode(req.Mode, "async")
		svc := reanalysis.New(st, reanalysis.Options{})
		switch mode {
		case "async":
			job, err := svc.EnqueueBatchReanalyze(opts)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusAccepted, reanalysisResponse{Job: analysisJobViewFromStore(job)})
		case "sync":
			result, err := svc.ReanalyzeBatch(r.Context(), opts)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, reanalysisResponse{Job: analysisJobViewFromStore(result.Job), Result: result})
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be sync or async"})
		}
	}
}

func analysisJobDetailAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(pathClean(r.URL.Path), "/api/analysis/jobs/")
		path = strings.Trim(path, "/")
		if path == "" {
			http.NotFound(w, r)
			return
		}
		parts := strings.Split(path, "/")
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid job id"})
			return
		}
		if len(parts) == 2 {
			if parts[1] == "cancel" && r.Method == http.MethodPost {
				if err := st.MarkAnalysisJobCanceled(id); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
				job, err := st.GetAnalysisJob(id)
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, analysisJobResponse{Job: analysisJobViewFromStore(job)})
				return
			}
			http.NotFound(w, r)
			return
		}
		if len(parts) != 1 || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		job, err := st.GetAnalysisJob(id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, analysisJobResponse{Job: analysisJobViewFromStore(job)})
	}
}

func overviewAPIHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if st == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store not configured"})
			return
		}

		windowLabel, since, bucketSize, bucketCount := parseOverviewWindow(r.URL.Query().Get("window"))
		dashboard, err := st.Overview(store.OverviewOptions{
			Since:       since,
			BucketSize:  bucketSize,
			BucketCount: bucketCount,
			Limit:       5,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "overview error: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, overviewResponseFromStore(windowLabel, dashboard))
	}
}

func traceAPIHandler(st *store.Store, rtr *router.Router) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(pathClean(r.URL.Path), "/api/traces/")
		path = strings.Trim(path, "/")
		if path == "" {
			http.NotFound(w, r)
			return
		}

		parts := strings.Split(path, "/")
		traceID := parts[0]
		entry, absPath, err := loadTrace(st, traceID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "trace not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			handleTraceDetail(w, absPath, entry, rtr)
		case len(parts) == 2 && parts[1] == "raw" && r.Method == http.MethodGet:
			handleTraceRaw(w, absPath, entry)
		case len(parts) == 2 && parts[1] == "observation" && r.Method == http.MethodGet:
			handleTraceObservation(w, st, entry)
		case len(parts) == 2 && parts[1] == "findings" && r.Method == http.MethodGet:
			handleTraceFindings(w, r, st, entry)
		case len(parts) == 2 && parts[1] == "performance" && r.Method == http.MethodGet:
			handleTracePerformance(w, entry)
		case len(parts) == 2 && parts[1] == "download" && r.Method == http.MethodGet:
			serveTraceDownload(w, r, absPath)
		case len(parts) == 2 && parts[1] == "reparse" && r.Method == http.MethodPost:
			handleTraceReparse(w, r, st, entry)
		case len(parts) == 2 && parts[1] == "scan" && r.Method == http.MethodPost:
			handleTraceScan(w, r, st, entry)
		case len(parts) == 2 && parts[1] == "repair-usage" && r.Method == http.MethodPost:
			handleTraceRepairUsage(w, r, st, entry)
		case len(parts) == 2 && parts[1] == "reanalyze" && r.Method == http.MethodPost:
			handleTraceReanalyze(w, r, st, entry)
		default:
			http.NotFound(w, r)
		}
	}
}

func handleTraceReparse(w http.ResponseWriter, r *http.Request, st *store.Store, entry store.LogEntry) {
	req, ok := decodeReanalysisRequest(w, r)
	if !ok {
		return
	}
	runTraceReanalysis(w, r, st, requestMode(req.Mode, "sync"), func(svc *reanalysis.Service) (store.AnalysisJobRecord, error) {
		return svc.EnqueueTraceReparse(entry.ID, reanalysis.TraceOptions{Scan: req.Scan})
	}, func(svc *reanalysis.Service) (reanalysis.Result, error) {
		return svc.ReparseTrace(r.Context(), entry.ID, reanalysis.TraceOptions{Scan: req.Scan})
	})
}

func handleTraceScan(w http.ResponseWriter, r *http.Request, st *store.Store, entry store.LogEntry) {
	req, ok := decodeReanalysisRequest(w, r)
	if !ok {
		return
	}
	runTraceReanalysis(w, r, st, requestMode(req.Mode, "sync"), func(svc *reanalysis.Service) (store.AnalysisJobRecord, error) {
		return svc.EnqueueTraceRescan(entry.ID)
	}, func(svc *reanalysis.Service) (reanalysis.Result, error) {
		return svc.RescanTrace(r.Context(), entry.ID)
	})
}

func handleTraceRepairUsage(w http.ResponseWriter, r *http.Request, st *store.Store, entry store.LogEntry) {
	req, ok := decodeReanalysisRequest(w, r)
	if !ok {
		return
	}
	if req.RewriteCassette && requestMode(req.Mode, "sync") == "async" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rewrite_cassette is only supported in sync mode"})
		return
	}
	runTraceReanalysis(w, r, st, requestMode(req.Mode, "sync"), func(svc *reanalysis.Service) (store.AnalysisJobRecord, error) {
		return svc.EnqueueTraceRepairUsage(entry.ID)
	}, func(svc *reanalysis.Service) (reanalysis.Result, error) {
		return svc.RepairTraceUsage(r.Context(), entry.ID, reanalysis.RepairUsageOptions{RewriteCassette: req.RewriteCassette})
	})
}

func handleTraceReanalyze(w http.ResponseWriter, r *http.Request, st *store.Store, entry store.LogEntry) {
	req, ok := decodeReanalysisRequest(w, r)
	if !ok {
		return
	}
	runTraceReanalysis(w, r, st, requestMode(req.Mode, "sync"), func(svc *reanalysis.Service) (store.AnalysisJobRecord, error) {
		return svc.EnqueueTraceReanalyze(entry.ID)
	}, func(svc *reanalysis.Service) (reanalysis.Result, error) {
		return svc.ReanalyzeTrace(r.Context(), entry.ID)
	})
}

func runTraceReanalysis(
	w http.ResponseWriter,
	r *http.Request,
	st *store.Store,
	mode string,
	enqueue func(*reanalysis.Service) (store.AnalysisJobRecord, error),
	runSync func(*reanalysis.Service) (reanalysis.Result, error),
) {
	svc := reanalysis.New(st, reanalysis.Options{})
	switch mode {
	case "async":
		job, err := enqueue(svc)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, reanalysisResponse{Job: analysisJobViewFromStore(job)})
	case "sync":
		result, err := runSync(svc)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, reanalysisResponse{Job: analysisJobViewFromStore(result.Job), Result: result})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be sync or async"})
	}
}

func decodeReanalysisRequest(w http.ResponseWriter, r *http.Request) (reanalysisRequest, bool) {
	var req reanalysisRequest
	if r.Body == nil || r.ContentLength == 0 {
		return req, true
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return reanalysisRequest{}, false
	}
	return req, true
}

func requestMode(mode string, fallback string) string {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		return fallback
	}
	return mode
}

func handleTraceObservation(w http.ResponseWriter, st *store.Store, entry store.LogEntry) {
	summary, err := st.GetObservationSummary(entry.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trace observation not found; run analyze reparse first"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	nodes, err := st.ListSemanticNodes(entry.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	tree := observe.RebuildNodeTree(nodes)
	payload := observationDetailResponse{
		ID:      entry.ID,
		Summary: observationSummaryFromStore(summary),
		Nodes:   observationNodeViewsFromFlat(nodes),
		Tree:    observationNodeViewsFromTree(tree, 0),
	}
	writeJSON(w, http.StatusOK, payload)
}

func handleTraceFindings(w http.ResponseWriter, r *http.Request, st *store.Store, entry store.LogEntry) {
	filter := store.FindingFilter{
		Category: strings.TrimSpace(r.URL.Query().Get("category")),
		Severity: strings.TrimSpace(r.URL.Query().Get("severity")),
	}
	findings, err := st.ListFindings(entry.ID, filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	items := make([]findingView, 0, len(findings))
	for _, finding := range findings {
		items = append(items, findingViewFromObservation(finding))
	}
	writeJSON(w, http.StatusOK, findingListResponse{
		ID:       entry.ID,
		Items:    items,
		Total:    len(items),
		Severity: filter.Severity,
		Category: filter.Category,
	})
}

func handleTracePerformance(w http.ResponseWriter, entry store.LogEntry) {
	writeJSON(w, http.StatusOK, performanceResponse{
		ID:          entry.ID,
		Scope:       "trace",
		Performance: buildTracePerformance(entry),
	})
}

func handleTraceDetail(w http.ResponseWriter, absPath string, entry store.LogEntry, rtr *router.Router) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	parsed, err := ParseLogFile(content)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "parse error: " + err.Error()})
		return
	}

	resp := detailResponse{
		ID:          entry.ID,
		Messages:    parsed.ChatMessages,
		Tools:       parsed.RequestTools,
		AIContent:   parsed.AIContent,
		AIReasoning: parsed.AIReasoning,
		AIBlocks:    parsed.AIBlocks,
		ToolCalls:   parsed.ResponseToolCalls,
		Performance: buildTracePerformance(entry),
		Header: recordHeaderView{
			Version: parsed.Header.Version,
			Meta:    parsed.Header.Meta,
			Layout:  parsed.Header.Layout,
			Usage:   parsed.Header.Usage,
		},
	}
	if entry.SessionID != "" {
		resp.Session = &traceSessionView{
			SessionID:     entry.SessionID,
			SessionSource: entry.SessionSource,
		}
	}
	if health := selectedUpstreamHealthView(rtr, entry.Header.Meta.SelectedUpstreamID); health != nil {
		resp.SelectedUpstreamHealth = health
	}
	resp.Events = buildTimelineEventViews(parsed)
	writeJSON(w, http.StatusOK, resp)
}

func handleTraceRaw(w http.ResponseWriter, absPath string, entry store.LogEntry) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	parsed, err := ParseLogFile(content)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "parse error: " + err.Error()})
		return
	}

	payload := rawDetailResponse{
		ID:               entry.ID,
		RequestProtocol:  parsed.ReqFull,
		ResponseProtocol: parsed.ResFull,
		Header: recordHeaderView{
			Version: parsed.Header.Version,
			Meta:    parsed.Header.Meta,
			Layout:  parsed.Header.Layout,
			Usage:   parsed.Header.Usage,
		},
		Events: toEventViewsFromRecord(parsed.Events),
	}
	writeJSON(w, http.StatusOK, payload)
}

func loadTrace(st *store.Store, traceID string) (store.LogEntry, string, error) {
	if st == nil {
		return store.LogEntry{}, "", errors.New("store not configured")
	}
	entry, err := st.GetByID(traceID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return store.LogEntry{}, "", os.ErrNotExist
		}
		if strings.Contains(err.Error(), "no rows") {
			return store.LogEntry{}, "", os.ErrNotExist
		}
		return store.LogEntry{}, "", err
	}
	absPath, err := filepath.Abs(entry.LogPath)
	if err != nil {
		return store.LogEntry{}, "", err
	}
	return entry, absPath, nil
}

func toEventViewsFromRecord(events []recordfile.RecordEvent) []recordEventView {
	if len(events) == 0 {
		return []recordEventView{}
	}
	payload := make([]recordEventView, 0, len(events))
	for _, event := range events {
		row := recordEventView{
			"type": event.Type,
			"time": event.Time,
		}
		if event.Method != "" {
			row["method"] = event.Method
		}
		if event.URL != "" {
			row["url"] = event.URL
		}
		if event.StatusCode != 0 {
			row["status_code"] = event.StatusCode
		}
		if event.IsStream {
			row["is_stream"] = event.IsStream
		}
		if event.HeaderBytes != 0 {
			row["header_bytes"] = event.HeaderBytes
		}
		if event.BodyBytes != 0 {
			row["body_bytes"] = event.BodyBytes
		}
		if event.Message != "" {
			row["message"] = event.Message
		}
		if len(event.Attributes) > 0 {
			row["attributes"] = event.Attributes
		}
		payload = append(payload, row)
	}
	return payload
}

func observationSummaryFromStore(summary store.ObservationSummary) observationSummaryView {
	var summaryPayload any = map[string]any{}
	if strings.TrimSpace(summary.SummaryJSON) != "" {
		_ = json.Unmarshal([]byte(summary.SummaryJSON), &summaryPayload)
	}
	var warningsPayload any = []any{}
	if strings.TrimSpace(summary.WarningsJSON) != "" {
		_ = json.Unmarshal([]byte(summary.WarningsJSON), &warningsPayload)
	}
	return observationSummaryView{
		TraceID:       summary.TraceID,
		Parser:        summary.Parser,
		ParserVersion: summary.ParserVersion,
		Status:        summary.Status,
		Provider:      summary.Provider,
		Operation:     summary.Operation,
		Model:         summary.Model,
		Summary:       summaryPayload,
		Warnings:      warningsPayload,
		CreatedAt:     summary.CreatedAt,
		UpdatedAt:     summary.UpdatedAt,
	}
}

func observationNodeViewsFromFlat(nodes []observe.FlatSemanticNode) []observationNodeView {
	out := make([]observationNodeView, 0, len(nodes))
	for _, row := range nodes {
		out = append(out, observationNodeViewFromNode(row.Node, row.ParentID, row.Depth))
	}
	return out
}

func observationNodeViewsFromTree(nodes []observe.SemanticNode, depth int) []observationNodeView {
	out := make([]observationNodeView, 0, len(nodes))
	for _, node := range nodes {
		view := observationNodeViewFromNode(node, node.ParentID, depth)
		view.Children = observationNodeViewsFromTree(node.Children, depth+1)
		out = append(out, view)
	}
	return out
}

func observationNodeViewFromNode(node observe.SemanticNode, parentID string, depth int) observationNodeView {
	return observationNodeView{
		ID:             node.ID,
		ParentID:       parentID,
		ProviderType:   node.ProviderType,
		NormalizedType: string(node.NormalizedType),
		Role:           node.Role,
		Path:           node.Path,
		Index:          node.Index,
		Depth:          depth,
		TextPreview:    node.Text,
		Raw:            node.Raw,
	}
}

func findingViewFromObservation(finding observe.Finding) findingView {
	return findingView{
		ID:              finding.ID,
		TraceID:         finding.TraceID,
		Category:        finding.Category,
		Severity:        string(finding.Severity),
		Confidence:      finding.Confidence,
		Title:           finding.Title,
		Description:     finding.Description,
		EvidencePath:    finding.EvidencePath,
		EvidenceExcerpt: finding.EvidenceExcerpt,
		NodeID:          finding.NodeID,
		Detector:        finding.Detector,
		DetectorVersion: finding.DetectorVersion,
		CreatedAt:       finding.CreatedAt,
	}
}

func analysisRunViews(runs []store.AnalysisRunRecord) []analysisRunView {
	out := make([]analysisRunView, 0, len(runs))
	for _, run := range runs {
		var output any = map[string]any{}
		if strings.TrimSpace(run.OutputJSON) != "" {
			_ = json.Unmarshal([]byte(run.OutputJSON), &output)
		}
		out = append(out, analysisRunView{
			ID:              run.ID,
			TraceID:         run.TraceID,
			SessionID:       run.SessionID,
			Kind:            run.Kind,
			Analyzer:        run.Analyzer,
			AnalyzerVersion: run.AnalyzerVersion,
			Model:           run.Model,
			InputRef:        run.InputRef,
			Output:          output,
			Status:          run.Status,
			CreatedAt:       run.CreatedAt,
		})
	}
	return out
}

func analysisJobViewFromStore(job store.AnalysisJobRecord) analysisJobView {
	var steps []string
	if strings.TrimSpace(job.StepsJSON) != "" {
		_ = json.Unmarshal([]byte(job.StepsJSON), &steps)
	}
	var request any = map[string]any{}
	if strings.TrimSpace(job.RequestJSON) != "" {
		_ = json.Unmarshal([]byte(job.RequestJSON), &request)
	}
	var result any = map[string]any{}
	if strings.TrimSpace(job.ResultJSON) != "" {
		_ = json.Unmarshal([]byte(job.ResultJSON), &result)
	}
	return analysisJobView{
		ID:         job.ID,
		JobType:    job.JobType,
		TargetType: job.TargetType,
		TargetID:   job.TargetID,
		Status:     job.Status,
		Steps:      steps,
		Request:    request,
		Result:     result,
		LastError:  job.LastError,
		Attempts:   job.Attempts,
		CreatedAt:  job.CreatedAt,
		UpdatedAt:  job.UpdatedAt,
		StartedAt:  job.StartedAt,
		FinishedAt: job.FinishedAt,
	}
}

func overviewResponseFromStore(window string, dashboard store.OverviewDashboard) overviewResponse {
	return overviewResponse{
		Window:      window,
		RefreshedAt: time.Now().UTC(),
		Summary: overviewSummaryView{
			RequestCount:   dashboard.Summary.RequestCount,
			SuccessRequest: dashboard.Summary.SuccessRequest,
			FailedRequest:  dashboard.Summary.FailedRequest,
			SuccessRate:    dashboard.Summary.SuccessRate,
			TotalTokens:    dashboard.Summary.TotalTokens,
			AvgTTFTMs:      dashboard.Summary.AvgTTFTMs,
			AvgDurationMs:  dashboard.Summary.AvgDurationMs,
			P95TTFTMs:      dashboard.Summary.P95TTFTMs,
			P95DurationMs:  dashboard.Summary.P95DurationMs,
			StreamCount:    dashboard.Summary.StreamCount,
			SessionCount:   dashboard.Summary.SessionCount,
		},
		Timeline: overviewTimelineViews(dashboard.Timeline),
		Breakdown: overviewBreakdownView{
			Models:                countItemViews(dashboard.Breakdown.Models),
			Providers:             countItemViews(dashboard.Breakdown.Providers),
			Endpoints:             countItemViews(dashboard.Breakdown.Endpoints),
			Upstreams:             countItemViews(dashboard.Breakdown.Upstreams),
			RoutingFailureReasons: countItemViews(dashboard.Breakdown.RoutingFailureReasons),
			FindingCategories:     countItemViews(dashboard.Breakdown.FindingCategories),
		},
		Attention: overviewAttentionView{
			RecentFailures:   traceListItemsFromEntries(dashboard.Attention.RecentFailures),
			HighRiskFindings: findingViewsFromObservations(dashboard.Attention.HighRiskFindings),
			RoutingFailures:  toRoutingFailureItems(dashboard.Attention.RoutingFailures),
			SlowTraces:       traceListItemsFromEntries(dashboard.Attention.SlowTraces),
		},
		Analysis: overviewAnalysisSummary{
			Total:  dashboard.Analysis.Total,
			Failed: dashboard.Analysis.Failed,
			Recent: analysisRunViews(dashboard.Analysis.Recent),
		},
		Observation: overviewObservationView{
			TotalObservations: dashboard.Observation.TotalObservations,
			Parsed:            dashboard.Observation.Parsed,
			Failed:            dashboard.Observation.Failed,
			Queued:            dashboard.Observation.Queued,
			Running:           dashboard.Observation.Running,
			Unparsed:          dashboard.Observation.Unparsed,
			RecentFailures:    parseJobViews(dashboard.Observation.RecentFailures),
		},
	}
}

func overviewTimelineViews(items []store.OverviewTimelineItem) []overviewTimelineItem {
	out := make([]overviewTimelineItem, 0, len(items))
	for _, item := range items {
		out = append(out, overviewTimelineItem{
			Time:          item.Time,
			RequestCount:  item.RequestCount,
			FailedRequest: item.FailedRequest,
			TotalTokens:   item.TotalTokens,
			AvgTTFTMs:     item.AvgTTFTMs,
			AvgDurationMs: item.AvgDurationMs,
		})
	}
	return out
}

func countItemViews(items []store.CountItem) []sessionCountItem {
	out := make([]sessionCountItem, 0, len(items))
	for _, item := range items {
		out = append(out, sessionCountItem{
			Label: item.Label,
			Count: item.Count,
		})
	}
	return out
}

func traceListItemsFromEntries(entries []store.LogEntry) []traceListItem {
	out := make([]traceListItem, 0, len(entries))
	for _, entry := range entries {
		out = append(out, traceListItemFromEntry(entry))
	}
	return out
}

func findingViewsFromObservations(findings []observe.Finding) []findingView {
	out := make([]findingView, 0, len(findings))
	for _, finding := range findings {
		out = append(out, findingViewFromObservation(finding))
	}
	return out
}

func parseJobViews(jobs []store.ParseJobRecord) []overviewParseJob {
	out := make([]overviewParseJob, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, overviewParseJob{
			ID:        job.ID,
			TraceID:   job.TraceID,
			Status:    job.Status,
			Attempts:  job.Attempts,
			LastError: job.LastError,
			CreatedAt: job.CreatedAt,
			UpdatedAt: job.UpdatedAt,
		})
	}
	return out
}

func tokenItemFromRecord(record auth.TokenRecord) tokenItem {
	return tokenItem{
		ID:         record.ID,
		Name:       record.Name,
		Prefix:     record.Prefix,
		Scope:      record.Scope,
		Enabled:    record.Enabled,
		Status:     tokenStatus(record),
		CreatedAt:  record.CreatedAt,
		ExpiresAt:  record.ExpiresAt,
		LastUsedAt: record.LastUsedAt,
	}
}

func tokenStatus(record auth.TokenRecord) string {
	if !record.Enabled {
		return "revoked"
	}
	if record.ExpiresAt != nil && time.Now().UTC().After(*record.ExpiresAt) {
		return "expired"
	}
	return "active"
}

func buildTimelineEventViews(parsed *ParsedData) []recordEventView {
	if parsed == nil {
		return []recordEventView{}
	}
	events := filterTimelineEvents(parsed.Events)
	if len(events) == 0 {
		return []recordEventView{}
	}
	views := toEventViewsFromRecord(events)
	for idx, event := range events {
		var items []timelineItemView
		switch event.Type {
		case "request":
			items = buildRequestTimelineItems(parsed)
		case "response":
			items = buildResponseTimelineItems(parsed)
		}
		if len(items) == 0 {
			continue
		}
		views[idx]["timeline_items"] = items
		views[idx]["message"] = firstNonEmpty(event.Message, renderTimelineTree(flattenTimelineItems(items)))
	}
	return views
}

func sessionSummaryItem(summary store.SessionSummary) sessionListItem {
	return sessionListItem{
		SessionID:      summary.SessionID,
		SessionSource:  summary.SessionSource,
		RequestCount:   summary.RequestCount,
		FirstSeen:      summary.FirstSeen,
		LastSeen:       summary.LastSeen,
		LastModel:      summary.LastModel,
		Providers:      summary.Providers,
		SuccessRequest: summary.SuccessRequest,
		FailedRequest:  summary.FailedRequest,
		SuccessRate:    summary.SuccessRate,
		TotalTokens:    summary.TotalTokens,
		AvgTTFT:        summary.AvgTTFT,
		TotalDuration:  summary.TotalDuration,
		StreamCount:    summary.StreamCount,
	}
}

func traceListItemFromEntry(entry store.LogEntry) traceListItem {
	return traceListItem{
		ID:               entry.ID,
		SessionID:        entry.SessionID,
		SessionSource:    entry.SessionSource,
		RecordedAt:       entry.Header.Meta.Time,
		Model:            entry.Header.Meta.Model,
		Provider:         entry.Header.Meta.Provider,
		Operation:        entry.Header.Meta.Operation,
		Endpoint:         entry.Header.Meta.Endpoint,
		Method:           entry.Header.Meta.Method,
		URL:              entry.Header.Meta.URL,
		StatusCode:       entry.Header.Meta.StatusCode,
		DurationMs:       entry.Header.Meta.DurationMs,
		TTFTMs:           entry.Header.Meta.TTFTMs,
		TotalTokens:      entry.Header.Usage.TotalTokens,
		PromptTokens:     entry.Header.Usage.PromptTokens,
		CompletionTokens: entry.Header.Usage.CompletionTokens,
		CachedTokens:     cachedTokens(entry),
		IsStream:         entry.Header.Layout.IsStream,
		Error:            entry.Header.Meta.Error,
	}
}

func systemEventViews(events []store.SystemEvent) []systemEventView {
	if len(events) == 0 {
		return []systemEventView{}
	}
	out := make([]systemEventView, 0, len(events))
	for _, event := range events {
		out = append(out, systemEventViewFromStore(event))
	}
	return out
}

func systemEventViewFromStore(event store.SystemEvent) systemEventView {
	details := json.RawMessage(`{}`)
	if len(event.DetailsJSON) > 0 {
		details = event.DetailsJSON
	}
	return systemEventView{
		ID:              event.ID,
		Fingerprint:     event.Fingerprint,
		Source:          event.Source,
		Category:        event.Category,
		Severity:        event.Severity,
		Status:          event.Status,
		Title:           event.Title,
		Message:         event.Message,
		Details:         details,
		TraceID:         event.TraceID,
		SessionID:       event.SessionID,
		JobID:           event.JobID,
		UpstreamID:      event.UpstreamID,
		Model:           event.Model,
		OccurrenceCount: event.OccurrenceCount,
		FirstSeenAt:     event.FirstSeenAt,
		LastSeenAt:      event.LastSeenAt,
		CreatedAt:       event.CreatedAt,
		UpdatedAt:       event.UpdatedAt,
		ReadAt:          optionalTime(event.ReadAt),
		ResolvedAt:      optionalTime(event.ResolvedAt),
	}
}

func systemEventSummaryView(summary store.SystemEventSummary, window string) systemEventSummaryResponse {
	return systemEventSummaryResponse{
		Total:      summary.Total,
		Unread:     summary.Unread,
		Critical:   summary.Critical,
		Error:      summary.Error,
		Warning:    summary.Warning,
		LastSeenAt: optionalTime(summary.LastSeenAt),
		BySource:   countItemViews(summary.BySource),
		ByCategory: countItemViews(summary.ByCategory),
		Window:     window,
	}
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func buildTracePerformance(entry store.LogEntry) performanceView {
	item := traceListItemFromEntry(entry)
	success := 0
	failed := 0
	if item.StatusCode >= 200 && item.StatusCode < 300 && strings.TrimSpace(item.Error) == "" {
		success = 1
	} else {
		failed = 1
	}
	return performanceView{
		RequestCount:       1,
		SuccessRequest:     success,
		FailedRequest:      failed,
		SuccessRate:        float64(success),
		DurationMs:         item.DurationMs,
		TTFTMs:             item.TTFTMs,
		TokensPerSec:       tokensPerSec(item.TotalTokens, item.DurationMs),
		TotalTokens:        item.TotalTokens,
		PromptTokens:       item.PromptTokens,
		CompletionTokens:   item.CompletionTokens,
		CachedTokens:       item.CachedTokens,
		CacheRatio:         cacheRatio(item.CachedTokens, item.PromptTokens),
		StatusCode:         item.StatusCode,
		ProviderError:      item.Error,
		IsStream:           item.IsStream,
		SelectedUpstreamID: entry.Header.Meta.SelectedUpstreamID,
		RoutingPolicy:      entry.Header.Meta.RoutingPolicy,
		RoutingFallback:    entry.Header.Meta.RoutingCandidateCount > 1,
	}
}

func buildAggregatePerformance(traces []traceListItem) performanceView {
	perf := performanceView{RequestCount: len(traces)}
	models := map[string]*perfAccumulator{}
	endpoints := map[string]*perfAccumulator{}
	for _, trace := range traces {
		addTraceToPerformance(&perf, trace)
		addTraceToAccumulator(models, firstNonEmpty(trace.Model, "unknown-model"), trace)
		addTraceToAccumulator(endpoints, firstNonEmpty(trace.Endpoint, trace.Operation, trace.URL, "unknown-endpoint"), trace)
	}
	finalizePerformance(&perf)
	perf.ByModel = perfCountItems(models)
	perf.ByEndpoint = perfCountItems(endpoints)
	return perf
}

func buildUpstreamPerformance(item upstreamItem) performanceView {
	perf := performanceView{
		RequestCount:   item.RequestCount,
		SuccessRequest: item.SuccessRequest,
		FailedRequest:  item.FailedRequest,
		SuccessRate:    item.SuccessRate,
		TotalTokens:    item.TotalTokens,
		TTFTMs:         int64(item.AvgTTFT),
	}
	perf.Upstreams = []upstreamPerf{{
		ID:             item.ID,
		BaseURL:        item.BaseURL,
		ProviderPreset: item.ProviderPreset,
		RequestCount:   item.RequestCount,
		SuccessRequest: item.SuccessRequest,
		FailedRequest:  item.FailedRequest,
		SuccessRate:    item.SuccessRate,
		TotalTokens:    item.TotalTokens,
		AvgTTFT:        item.AvgTTFT,
		HealthState:    item.HealthState,
		ErrorRate:      item.ErrorRate,
		TimeoutRate:    item.TimeoutRate,
	}}
	return perf
}

type perfAccumulator struct {
	count        int
	success      int
	totalTokens  int
	totalTTFT    int64
	totalLatency int64
}

func addTraceToPerformance(perf *performanceView, trace traceListItem) {
	perf.DurationMs += trace.DurationMs
	perf.TTFTMs += trace.TTFTMs
	perf.TotalTokens += trace.TotalTokens
	perf.PromptTokens += trace.PromptTokens
	perf.CompletionTokens += trace.CompletionTokens
	perf.CachedTokens += trace.CachedTokens
	if trace.StatusCode >= 200 && trace.StatusCode < 300 && strings.TrimSpace(trace.Error) == "" {
		perf.SuccessRequest++
	} else {
		perf.FailedRequest++
	}
}

func finalizePerformance(perf *performanceView) {
	totalDuration := perf.DurationMs
	if perf.RequestCount > 0 {
		perf.SuccessRate = float64(perf.SuccessRequest) / float64(perf.RequestCount)
		perf.DurationMs = perf.DurationMs / int64(perf.RequestCount)
		perf.TTFTMs = perf.TTFTMs / int64(perf.RequestCount)
	}
	perf.TokensPerSec = tokensPerSec(perf.TotalTokens, totalDuration)
	perf.CacheRatio = cacheRatio(perf.CachedTokens, perf.PromptTokens)
}

func addTraceToAccumulator(items map[string]*perfAccumulator, key string, trace traceListItem) {
	acc := items[key]
	if acc == nil {
		acc = &perfAccumulator{}
		items[key] = acc
	}
	acc.count++
	acc.totalTokens += trace.TotalTokens
	acc.totalTTFT += trace.TTFTMs
	acc.totalLatency += trace.DurationMs
	if trace.StatusCode >= 200 && trace.StatusCode < 300 && strings.TrimSpace(trace.Error) == "" {
		acc.success++
	}
}

func perfCountItems(items map[string]*perfAccumulator) []perfCountItem {
	out := make([]perfCountItem, 0, len(items))
	for label, acc := range items {
		item := perfCountItem{Label: label, Count: acc.count, TotalTokens: acc.totalTokens}
		if acc.count > 0 {
			item.AvgDuration = acc.totalLatency / int64(acc.count)
			item.AvgTTFT = acc.totalTTFT / int64(acc.count)
			item.SuccessRate = float64(acc.success) / float64(acc.count)
			item.TokensPerSec = tokensPerSec(acc.totalTokens, acc.totalLatency)
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}

func tokensPerSec(tokens int, durationMs int64) float64 {
	if tokens <= 0 || durationMs <= 0 {
		return 0
	}
	return float64(tokens) * 1000 / float64(durationMs)
}

func cacheRatio(cached int, prompt int) float64 {
	if cached <= 0 || prompt <= 0 {
		return 0
	}
	return float64(cached) / float64(prompt)
}

func parseListFilter(r *http.Request) store.ListFilter {
	if r == nil {
		return store.ListFilter{}
	}
	query := r.URL.Query()
	return store.ListFilter{
		Query:            strings.TrimSpace(query.Get("q")),
		Provider:         strings.TrimSpace(query.Get("provider")),
		Model:            strings.TrimSpace(query.Get("model")),
		Endpoint:         strings.TrimSpace(query.Get("endpoint")),
		SelectedUpstream: strings.TrimSpace(query.Get("upstream")),
		Status:           strings.TrimSpace(query.Get("status")),
		MissingUsage:     parseBool(query.Get("missing_usage")),
		MinDurationMs:    int64(parseInt(query.Get("min_duration_ms"), 0)),
		MaxDurationMs:    int64(parseInt(query.Get("max_duration_ms"), 0)),
		MinTTFTMs:        int64(parseInt(query.Get("min_ttft_ms"), 0)),
		MaxTTFTMs:        int64(parseInt(query.Get("max_ttft_ms"), 0)),
		MinTokens:        parseInt(query.Get("min_tokens"), 0),
		MaxTokens:        parseInt(query.Get("max_tokens"), 0),
	}
}

func buildSessionBreakdown(traces []traceListItem) sessionBreakdownView {
	modelCounts := map[string]int{}
	endpointCounts := map[string]int{}
	failed := 0
	for _, trace := range traces {
		model := firstNonEmpty(trace.Model, "unknown-model")
		endpoint := firstNonEmpty(trace.Endpoint, trace.Operation, trace.URL, "unknown-endpoint")
		modelCounts[model]++
		endpointCounts[endpoint]++
		if trace.StatusCode < 200 || trace.StatusCode >= 300 {
			failed++
		}
	}
	return sessionBreakdownView{
		Models:       sortSessionCounts(modelCounts),
		Endpoints:    sortSessionCounts(endpointCounts),
		FailedTraces: failed,
	}
}

func sortSessionCounts(counts map[string]int) []sessionCountItem {
	items := make([]sessionCountItem, 0, len(counts))
	for label, count := range counts {
		items = append(items, sessionCountItem{Label: label, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Label < items[j].Label
	})
	return items
}

func toSessionCountItems(items []store.CountItem) []sessionCountItem {
	out := make([]sessionCountItem, 0, len(items))
	for _, item := range items {
		out = append(out, sessionCountItem{
			Label: item.Label,
			Count: item.Count,
		})
	}
	return out
}

func buildSessionTimeline(traces []traceListItem) []sessionTimelineItem {
	items := make([]sessionTimelineItem, 0, len(traces))
	for _, trace := range traces {
		items = append(items, sessionTimelineItem{
			TraceID:     trace.ID,
			Time:        trace.RecordedAt,
			Model:       trace.Model,
			Provider:    trace.Provider,
			Endpoint:    trace.Endpoint,
			StatusCode:  trace.StatusCode,
			DurationMs:  trace.DurationMs,
			TTFTMs:      trace.TTFTMs,
			TotalTokens: trace.TotalTokens,
			IsStream:    trace.IsStream,
			Error:       trace.Error,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].Time.Equal(items[j].Time) {
			return items[i].Time.Before(items[j].Time)
		}
		return items[i].TraceID < items[j].TraceID
	})
	return items
}

func filterTimelineEvents(events []recordfile.RecordEvent) []recordfile.RecordEvent {
	if len(events) == 0 {
		return nil
	}
	filtered := make([]recordfile.RecordEvent, 0, len(events))
	for _, event := range events {
		if strings.HasSuffix(event.Type, ".delta") {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

func buildRequestTimelineItems(parsed *ParsedData) []timelineItemView {
	if parsed == nil || len(parsed.ChatMessages) == 0 {
		return nil
	}

	var items []timelineItemView
	for _, message := range parsed.ChatMessages {
		if item, ok := timelineItemForChatMessage(message); ok {
			items = append(items, item)
		}
	}
	return items
}

func buildResponseTimelineItems(parsed *ParsedData) []timelineItemView {
	if parsed == nil {
		return nil
	}

	var items []timelineItemView
	if parsed.AIReasoning != "" {
		body := timelineCompact(parsed.AIReasoning)
		items = append(items, timelineItemView{
			Kind:    "thinking",
			Label:   "Thinking",
			Summary: timelinePreview(body),
			Body:    body,
		})
	}
	for _, call := range parsed.ResponseToolCalls {
		items = append(items, timelineToolCallItem(call, "tool call"))
	}
	if parsed.AIContent != "" {
		body := timelineCompact(parsed.AIContent)
		items = append(items, timelineItemView{
			Kind:    "output",
			Label:   "Final output",
			Summary: timelinePreview(body),
			Body:    body,
		})
	}
	for _, block := range parsed.AIBlocks {
		summary := firstNonEmpty(block.Text, block.Meta, block.URL, block.FileID)
		if summary == "" {
			continue
		}
		items = append(items, timelineItemView{
			Kind:    firstNonEmpty(block.Kind, "block"),
			Label:   firstNonEmpty(block.Title, block.Kind, "output"),
			Summary: timelinePreview(summary),
			Body:    timelineCompact(summary),
		})
	}
	return items
}

func timelineItemForChatMessage(message ChatMessage) (timelineItemView, bool) {
	role := firstNonEmpty(message.Role, "message")
	switch message.MessageType {
	case "tool_result", "function_call_output":
		return timelineToolResultItem(message), true
	}

	item := timelineItemView{
		Kind:  "message",
		Label: timelineRoleLabel(role),
		Role:  role,
	}
	if message.Content != "" {
		body := timelineCompact(message.Content)
		item.Summary = timelinePreview(body)
		item.Body = body
	}
	for _, block := range message.Blocks {
		summary := firstNonEmpty(block.Text, block.Meta, block.URL, block.FileID)
		if summary == "" {
			continue
		}
		item.Children = append(item.Children, timelineItemView{
			Kind:    firstNonEmpty(block.Kind, "block"),
			Label:   firstNonEmpty(block.Title, block.Kind, "block"),
			Summary: timelinePreview(summary),
			Body:    timelineCompact(summary),
		})
	}
	for _, call := range message.ToolCalls {
		label := "tool call"
		if role == "assistant" && strings.Contains(message.MessageType, "function_call") {
			label = "function call"
		}
		item.Children = append(item.Children, timelineToolCallItem(call, label))
	}
	if item.Summary == "" && len(item.Children) == 0 {
		return timelineItemView{}, false
	}
	return item, true
}

func timelineToolCallItem(call ToolCall, label string) timelineItemView {
	body := timelineCompact(call.Function.Arguments)
	if body == "" {
		body = "{}"
	}
	return timelineItemView{
		Kind:    "tool_call",
		Label:   label,
		Name:    firstNonEmpty(call.Function.Name, call.ID, "tool"),
		ID:      call.ID,
		Summary: timelinePreview(body),
		Body:    body,
	}
}

func timelineToolResultItem(message ChatMessage) timelineItemView {
	body := timelineCompact(message.Content)
	if body == "" {
		body = "(empty)"
	}
	status := "ok"
	if message.IsError {
		status = "error"
	}
	return timelineItemView{
		Kind:    "tool_response",
		Label:   "tool response",
		Name:    firstNonEmpty(message.Name, message.ToolCallID, "tool"),
		ID:      message.ToolCallID,
		Status:  status,
		Summary: timelinePreview(body),
		Body:    body,
	}
}

func flattenTimelineItems(items []timelineItemView) []string {
	if len(items) == 0 {
		return nil
	}
	var lines []string
	for _, item := range items {
		lines = append(lines, flattenTimelineItem(item)...)
	}
	return lines
}

func flattenTimelineItem(item timelineItemView) []string {
	if item.Kind == "message" {
		line := firstNonEmpty(item.Role, item.Label, "message")
		if item.Summary != "" {
			line += ": " + item.Summary
		}
		lines := []string{line}
		for _, child := range item.Children {
			lines = append(lines, "  "+flattenTimelineItemLine(child))
		}
		return lines
	}
	return []string{flattenTimelineItemLine(item)}
}

func flattenTimelineItemLine(item timelineItemView) string {
	switch item.Kind {
	case "tool_call":
		line := fmt.Sprintf("%s %s", firstNonEmpty(item.Label, "tool call"), firstNonEmpty(item.Name, "tool"))
		if item.ID != "" {
			line += fmt.Sprintf(" [%s]", item.ID)
		}
		if item.Summary != "" {
			line += ": " + item.Summary
		}
		return line
	case "tool_response":
		line := fmt.Sprintf("%s %s", firstNonEmpty(item.Label, "tool response"), firstNonEmpty(item.Name, "tool"))
		if item.ID != "" {
			line += fmt.Sprintf(" [%s]", item.ID)
		}
		if item.Summary != "" {
			line += ": " + item.Summary
		}
		if item.Status == "error" {
			line += " [error]"
		}
		return line
	default:
		line := firstNonEmpty(item.Label, item.Kind, "item")
		if item.Summary != "" {
			line += ": " + item.Summary
		}
		return line
	}
}

func renderTimelineTree(items []string) string {
	if len(items) == 0 {
		return ""
	}
	lines := make([]string, 0, len(items))
	for idx, item := range items {
		prefix := "├─ "
		if idx == len(items)-1 {
			prefix = "└─ "
		}
		lines = append(lines, prefix+item)
	}
	return strings.Join(lines, "\n")
}

func timelinePreview(value string) string {
	compact := timelineCompact(value)
	if compact == "" {
		return ""
	}
	const limit = 180
	runes := []rune(compact)
	if len(runes) <= limit {
		return compact
	}
	return string(runes[:limit-1]) + "…"
}

func timelineCompact(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func timelineRoleLabel(role string) string {
	switch role {
	case "system":
		return "System"
	case "user":
		return "User"
	case "assistant":
		return "Assistant"
	case "tool":
		return "Tool"
	default:
		if role == "" {
			return "Message"
		}
		return role
	}
}

func serveTraceDownload(w http.ResponseWriter, r *http.Request, absPath string) {
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(absPath)))
	http.ServeFile(w, r, absPath)
}

func cachedTokens(entry store.LogEntry) int {
	if entry.Header.Usage.PromptTokenDetails == nil {
		return 0
	}
	return entry.Header.Usage.PromptTokenDetails.CachedTokens
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeSystemEventStreamMessage(w http.ResponseWriter, eventType string, notification store.SystemEventNotification, st *store.Store) {
	summary, err := st.SystemEventSummary(time.Time{})
	if err != nil {
		payload, _ := json.Marshal(map[string]string{"error": err.Error()})
		_, _ = fmt.Fprintf(w, "event: system_event.error\ndata: %s\n\n", payload)
		return
	}
	summaryView := systemEventSummaryView(summary, "all")
	payload := systemEventStreamMessage{
		Type:       eventType,
		EventID:    notification.EventID,
		Status:     notification.Status,
		Severity:   notification.Severity,
		Source:     notification.Source,
		Category:   notification.Category,
		Summary:    summaryView,
		Unread:     summaryView.Unread,
		LastSeenAt: summaryView.LastSeenAt,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
}

func parseInt(v string, fallback int) int {
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func pathClean(v string) string {
	if v == "" {
		return "/"
	}
	clean := filepath.ToSlash(filepath.Clean(v))
	if !strings.HasPrefix(clean, "/") {
		clean = "/" + clean
	}
	return clean
}
