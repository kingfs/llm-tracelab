package monitor

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
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

type detailResponse struct {
	ID          string            `json:"id"`
	Header      recordHeaderView  `json:"header"`
	Events      []recordEventView `json:"events"`
	Messages    []ChatMessage     `json:"messages"`
	Tools       []RequestTool     `json:"tools"`
	AIContent   string            `json:"ai_content"`
	AIReasoning string            `json:"ai_reasoning"`
	AIBlocks    []ContentBlock    `json:"ai_blocks"`
	ToolCalls   []ToolCall        `json:"tool_calls"`
}

type rawDetailResponse struct {
	ID               string            `json:"id"`
	RequestProtocol  string            `json:"request_protocol"`
	ResponseProtocol string            `json:"response_protocol"`
	Header           recordHeaderView  `json:"header"`
	Events           []recordEventView `json:"events"`
}

type recordHeaderView struct {
	Version string      `json:"version"`
	Meta    interface{} `json:"meta"`
	Layout  interface{} `json:"layout"`
	Usage   interface{} `json:"usage"`
}

type recordEventView map[string]interface{}

type LogStats struct {
	TotalRequest   int     `json:"total_request"`
	AvgTTFT        int     `json:"avg_ttft"`
	TotalTokens    int     `json:"total_tokens"`
	SuccessRequest int     `json:"success_request"`
	FailedRequest  int     `json:"failed_request"`
	SuccessRate    float64 `json:"success_rate"`
}

func RegisterRoutes(mux *http.ServeMux, st *store.Store) {
	mux.HandleFunc("/api/traces", listAPIHandler(st))
	mux.HandleFunc("/api/traces/", traceAPIHandler(st))
	mux.Handle("/", appHandler())
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
		if err := st.Sync(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "sync error: " + err.Error()})
			return
		}

		page := parseInt(r.URL.Query().Get("page"), 1)
		pageSize := parseInt(r.URL.Query().Get("page_size"), 50)
		result, err := st.ListPage(page, pageSize)
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

func traceAPIHandler(st *store.Store) http.HandlerFunc {
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
			handleTraceDetail(w, absPath, entry)
		case len(parts) == 2 && parts[1] == "raw" && r.Method == http.MethodGet:
			handleTraceRaw(w, absPath, entry)
		case len(parts) == 2 && parts[1] == "download" && r.Method == http.MethodGet:
			http.ServeFile(w, r, absPath)
		default:
			http.NotFound(w, r)
		}
	}
}

func handleTraceDetail(w http.ResponseWriter, absPath string, entry store.LogEntry) {
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
		Header: recordHeaderView{
			Version: parsed.Header.Version,
			Meta:    parsed.Header.Meta,
			Layout:  parsed.Header.Layout,
			Usage:   parsed.Header.Usage,
		},
	}
	resp.Events = toEventViewsFromRecord(parsed.Events)
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
