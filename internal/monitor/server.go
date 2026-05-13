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
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
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

type traceListItem struct {
	ID               string    `json:"id"`
	SessionID        string    `json:"session_id,omitempty"`
	SessionSource    string    `json:"session_source,omitempty"`
	RecordedAt       time.Time `json:"recorded_at"`
	Model            string    `json:"model"`
	Provider         string    `json:"provider"`
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
	Traces      []traceListItem       `json:"traces"`
}

type sessionBreakdownView struct {
	Models       []sessionCountItem `json:"models"`
	Endpoints    []sessionCountItem `json:"endpoints"`
	FailedTraces int                `json:"failed_traces"`
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
	Router       *router.Router
	AuthVerifier auth.TokenVerifier
	AuthStore    *auth.Store
	SessionTTL   time.Duration
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token  string `json:"token"`
	Prefix string `json:"prefix"`
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

func RegisterRoutes(mux *http.ServeMux, st *store.Store, opts ...RouteOptions) {
	var opt RouteOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	mux.HandleFunc("/api/auth/status", authStatusAPIHandler(opt.AuthVerifier))
	mux.HandleFunc("/api/auth/login", authLoginAPIHandler(opt.AuthStore, opt.SessionTTL))
	mux.HandleFunc("/api/auth/check", monitorAuthRequired(authCheckAPIHandler(), opt.AuthVerifier))
	mux.HandleFunc("/api/auth/tokens", monitorAuthRequired(authCreateTokenAPIHandler(opt.AuthStore), opt.AuthVerifier))
	mux.HandleFunc("/api/traces", monitorAuthRequired(listAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/traces/", monitorAuthRequired(traceAPIHandler(st, opt.Router), opt.AuthVerifier))
	mux.HandleFunc("/api/sessions", monitorAuthRequired(sessionListAPIHandler(st), opt.AuthVerifier))
	mux.HandleFunc("/api/sessions/", monitorAuthRequired(sessionDetailAPIHandler(st), opt.AuthVerifier))
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

func authCreateTokenAPIHandler(authStore *auth.Store) http.HandlerFunc {
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

		sessionID := strings.TrimPrefix(pathClean(r.URL.Path), "/api/sessions/")
		sessionID = strings.Trim(sessionID, "/")
		if sessionID == "" || strings.Contains(sessionID, "/") {
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
		writeJSON(w, http.StatusOK, resp)
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
		default:
			http.NotFound(w, r)
		}
	}
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
		Query:    strings.TrimSpace(query.Get("q")),
		Provider: strings.TrimSpace(query.Get("provider")),
		Model:    strings.TrimSpace(query.Get("model")),
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
