package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/chaos"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/recorder"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/llm"
)

type aggregatedModelListResponse struct {
	Object string                     `json:"object,omitempty"`
	Data   []aggregatedModelListEntry `json:"data"`
}

type aggregatedModelListEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type contextKey string

const (
	logInfoContextKey   contextKey = "log_info"
	selectionContextKey contextKey = "router_selection"
)

// ensureStreamOptions 检查请求体，如果是 stream 模式，强制注入 stream_options
func ensureStreamOptions(req *http.Request) {
	_, _ = readAndNormalizeRequestBody(req)
}

func readAndNormalizeRequestBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}

	bodyBytes = injectStreamOptions(req, bodyBytes)
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	req.ContentLength = int64(len(bodyBytes))
	if len(bodyBytes) > 0 {
		req.Header.Set("Content-Length", fmt.Sprint(len(bodyBytes)))
	}
	return bodyBytes, nil
}

func injectStreamOptions(req *http.Request, bodyBytes []byte) []byte {
	// 1. 只有 POST 请求且 Content-Type 为 JSON 才处理
	if req.Method != http.MethodPost || !strings.Contains(req.Header.Get("Content-Type"), "application/json") {
		return bodyBytes
	}
	if llm.NormalizeEndpoint(req.URL.Path) != "/v1/chat/completions" {
		return bodyBytes
	}

	// 3. 解析 JSON
	// 使用 map[string]interface{} 以保留原始结构
	var payload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return bodyBytes // 不是 JSON，放弃
	}

	// 4. 检查 stream 字段
	isStream, ok := payload["stream"].(bool)
	if !ok || !isStream {
		return bodyBytes // 不是 stream 模式，放弃
	}

	// 5. 检查并注入 stream_options
	// 逻辑：如果 stream_options 不存在，或者存在但 include_usage 不为 true，则修改
	updated := false
	if opts, ok := payload["stream_options"].(map[string]interface{}); ok {
		if val, ok := opts["include_usage"].(bool); !ok || !val {
			opts["include_usage"] = true
			payload["stream_options"] = opts
			updated = true
		}
	} else {
		// 不存在 stream_options，创建之
		payload["stream_options"] = map[string]interface{}{
			"include_usage": true,
		}
		updated = true
	}

	// 6. 如果有修改，重新序列化并赋值给 req.Body
	if updated {
		newBytes, err := json.Marshal(payload)
		if err == nil {
			return newBytes
		}
	}
	return bodyBytes
}

// UsageSniffer 纯粹的嗅探器，不再做估算
type UsageSniffer struct {
	Source   io.ReadCloser
	File     io.Writer
	Count    *int64
	Usage    *recorder.UsageInfo
	Pipeline *llm.ResponsePipeline
	Events   *[]recorder.RecordEvent
}

func (s *UsageSniffer) Read(p []byte) (n int, err error) {
	n, err = s.Source.Read(p)
	if n > 0 {
		data := p[:n]

		// 1. 写入日志文件并计数
		if s.File != nil {
			if written, wErr := s.File.Write(data); wErr == nil {
				*s.Count += int64(written)
			}
		}

		if s.Pipeline != nil {
			s.Pipeline.Feed(data)
			if usage, ok := s.Pipeline.Usage(); ok && s.Usage != nil {
				*s.Usage = recorder.UsageInfo(usage)
			}
		}
	}
	return
}

func (s *UsageSniffer) Close() error {
	if s.Pipeline != nil {
		s.Pipeline.Finalize()
		if usage, ok := s.Pipeline.Usage(); ok && s.Usage != nil {
			*s.Usage = recorder.UsageInfo(usage)
		}
		if s.Events != nil {
			events := s.Pipeline.Events()
			*s.Events = make([]recorder.RecordEvent, len(events))
			copy(*s.Events, events)
		}
	}
	return s.Source.Close()
}

// InstrumentedResponseWriter 保持不变
type InstrumentedResponseWriter struct {
	w            http.ResponseWriter
	statusCode   int
	bytesWritten int64
	startTime    time.Time
	ttft         int64
	firstByte    bool
}

func NewInstrumentedResponseWriter(w http.ResponseWriter) *InstrumentedResponseWriter {
	return &InstrumentedResponseWriter{
		w:          w,
		statusCode: 200,
		startTime:  time.Now(),
		firstByte:  true,
		ttft:       -1,
	}
}
func (rw *InstrumentedResponseWriter) Header() http.Header { return rw.w.Header() }
func (rw *InstrumentedResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.w.WriteHeader(code)
}
func (rw *InstrumentedResponseWriter) Write(b []byte) (int, error) {
	if rw.firstByte {
		rw.ttft = time.Since(rw.startTime).Milliseconds()
		rw.firstByte = false
	}
	n, err := rw.w.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}
func (rw *InstrumentedResponseWriter) Flush() {
	if f, ok := rw.w.(http.Flusher); ok {
		f.Flush()
	}
}
func (rw *InstrumentedResponseWriter) GetMetrics() (int, int64, int64) {
	return rw.statusCode, rw.bytesWritten, rw.ttft
}

type Handler struct {
	proxy        *httputil.ReverseProxy
	recorder     *recorder.Recorder
	chaosManager *chaos.Manager
	cfg          *config.Config
	router       *router.Router
	authVerifier auth.TokenVerifier
}

func NewHandler(cfg *config.Config, st *store.Store, provided ...*router.Router) (*Handler, error) {
	var rtr *router.Router
	if len(provided) > 0 {
		rtr = provided[0]
	}
	var err error
	if rtr == nil {
		rtr, err = router.New(cfg, st)
		if err != nil {
			return nil, fmt.Errorf("build router: %w", err)
		}
		if err := rtr.Initialize(); err != nil {
			return nil, fmt.Errorf("initialize router: %w", err)
		}
	}

	rec := recorder.New(cfg.Debug.OutputDir, cfg.Debug.MaskKey, st)
	cm := chaos.New(cfg)

	rp := &httputil.ReverseProxy{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          200,
			MaxIdleConnsPerHost:   100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	rp.Director = func(req *http.Request) {
		selection, ok := req.Context().Value(selectionContextKey).(*router.Selection)
		if !ok || selection == nil || selection.Target == nil {
			return
		}
		clientPath := req.URL.Path
		if req.URL.RawQuery != "" {
			clientPath += "?" + req.URL.RawQuery
		}
		targetURL, err := url.Parse(selection.Target.Upstream.BaseURL)
		if err == nil {
			req.Host = targetURL.Host
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
		}
		fullURL, err := selection.Target.Upstream.BuildURL(clientPath)
		if err == nil {
			if parsed, parseErr := url.Parse(fullURL); parseErr == nil {
				req.URL.Path = parsed.Path
				req.URL.RawPath = parsed.RawPath
				req.URL.RawQuery = parsed.RawQuery
			}
		}
		selection.Target.Upstream.ApplyAuthHeaders(req.Header)
		req.Header.Set("Accept-Encoding", "identity")
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		logInfo, ok := resp.Request.Context().Value(logInfoContextKey).(*recorder.LogInfo)
		if !ok || logInfo == nil {
			return nil
		}

		// 1. 写入分隔符
		logInfo.File.Write([]byte("\n"))

		// 2. 写入 Header
		headerBuf := bytes.NewBufferString(fmt.Sprintf("%s %s\r\n", resp.Proto, resp.Status))
		resp.Header.Write(headerBuf)
		headerBuf.WriteString("\r\n")

		n, _ := logInfo.File.Write(headerBuf.Bytes())
		logInfo.Header.Layout.ResHeaderLen = int64(n)

		// 3. 判断 Stream
		isStream := llm.DetectStreamingResponse(resp.Header)
		if isStream {
			logInfo.Header.Layout.IsStream = true
		}

		// 4. 劫持 Body
		resp.Body = &UsageSniffer{
			Source:   resp.Body,
			File:     logInfo.File,
			Count:    &logInfo.Header.Layout.ResBodyLen,
			Usage:    &logInfo.Header.Usage,
			Pipeline: llm.NewResponsePipeline(logInfo.Header.Meta.Provider, logInfo.Header.Meta.Endpoint, isStream),
			Events:   &logInfo.Events,
		}
		return nil
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Error("Proxy error", "err", err)
		if logInfo, ok := r.Context().Value(logInfoContextKey).(*recorder.LogInfo); ok && logInfo != nil {
			logInfo.Header.Meta.Error = err.Error()
		}
		http.Error(w, "Proxy Error: "+err.Error(), http.StatusBadGateway)
	}

	return &Handler{
		proxy:        rp,
		recorder:     rec,
		chaosManager: cm,
		cfg:          cfg,
		router:       rtr,
	}, nil
}

func NewHandlerWithAuth(cfg *config.Config, st *store.Store, rtr *router.Router, verifier auth.TokenVerifier) (*Handler, error) {
	h, err := NewHandler(cfg, st, rtr)
	if err != nil {
		return nil, err
	}
	h.authVerifier = verifier
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if !auth.RequestAuthorized(r, h.authVerifier) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="llm-tracelab-proxy"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if llm.NormalizeEndpoint(r.URL.Path) == "/v1/models" {
		h.serveAggregatedModelList(w, r, start)
		return
	}

	// [Step 1] 读取请求体并自动注入 stream_options，后续 router/recorder 复用同一份 bytes。
	bodyBytes, err := readAndNormalizeRequestBody(r)
	if err != nil {
		slog.Error("Failed to read request body", "error", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	selection, err := h.router.SelectWithBody(r, bodyBytes)
	if err != nil {
		slog.Error("Failed to select upstream target", "error", err)
		h.recordSelectionFailureWithBody(r, start, http.StatusBadGateway, err, bodyBytes)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// [Step 2] 准备日志 (此时 r.Body 已经包含 injected options，Log 里会记录下来)
	logInfo, err := h.recorder.PrepareLogFileWithOptionsAndBody(r, recorder.PrepareOptions{
		SiteURL:                        selection.Target.Upstream.BaseURL,
		SelectedUpstreamID:             selection.Target.ID,
		SelectedUpstreamProviderPreset: selection.Target.Upstream.ProviderPreset,
		RoutingPolicy:                  h.routerPolicy(),
		RoutingScore:                   selection.Score,
		RoutingCandidateCount:          selection.CandidateCount,
	}, bodyBytes)
	if err != nil {
		slog.Error("Failed to prepare log file", "err", err)
		http.Error(w, "Internal Logging Error", 500)
		h.router.Complete(selection, router.Outcome{
			Success:    false,
			StatusCode: http.StatusInternalServerError,
			Stream:     selection.Request.Stream,
		})
		return
	}
	logInfo.Events = append(logInfo.Events, recorder.RecordEvent{
		Type: "routing.selection",
		Time: start,
		Attributes: map[string]interface{}{
			"upstream_id":       selection.Target.ID,
			"provider_preset":   selection.Target.Upstream.ProviderPreset,
			"candidate_count":   selection.CandidateCount,
			"routing_score":     selection.Score,
			"routing_policy":    h.routerPolicy(),
			"candidate_targets": selection.Candidates,
		},
	})

	irw := NewInstrumentedResponseWriter(w)

	defer func() {
		duration := time.Since(start)
		code, written, ttft := irw.GetMetrics()

		logInfo.Header.Meta.DurationMs = duration.Milliseconds()
		logInfo.Header.Meta.StatusCode = code
		logInfo.Header.Meta.ContentLength = written
		logInfo.Header.Meta.TTFTMs = ttft

		// 回填 Header
		if uErr := h.recorder.UpdateLogFile(logInfo); uErr != nil {
			slog.Error("Failed to update log file", "path", logInfo.Path, "err", uErr)
		}
		h.router.Complete(selection, router.Outcome{
			Success:        code >= 200 && code < 300 && logInfo.Header.Meta.Error == "",
			ClientCanceled: r.Context().Err() != nil,
			StatusCode:     code,
			DurationMs:     float64(duration.Milliseconds()),
			TTFTMs:         float64(ttft),
			Stream:         logInfo.Header.Layout.IsStream || selection.Request.Stream,
		})

		slog.Info("Request completed",
			"model", logInfo.Header.Meta.Model,
			"selected_upstream_id", logInfo.Header.Meta.SelectedUpstreamID,
			"status", code,
			"tokens_total", logInfo.Header.Usage.TotalTokens, // 此时应该有值了
		)
	}()

	// Chaos Logic ... (略，与之前一致，可保留)
	chaosRes := h.chaosManager.Evaluate(logInfo.Header.Meta.Model)
	if chaosRes.ShouldInject {
		// ... 保持之前的 chaos 逻辑 ...
		// 为节省篇幅这里简写，请保留你现有的代码
		if chaosRes.Action == "delay" {
			time.Sleep(chaosRes.Delay)
		}
		// ...
	}

	ctx := context.WithValue(r.Context(), logInfoContextKey, logInfo)
	ctx = context.WithValue(ctx, selectionContextKey, selection)
	h.proxy.ServeHTTP(irw, r.WithContext(ctx))
}

func (h *Handler) serveAggregatedModelList(w http.ResponseWriter, r *http.Request, start time.Time) {
	if h == nil || h.router == nil {
		http.Error(w, "router unavailable", http.StatusBadGateway)
		return
	}

	models := h.router.AggregatedModels()
	payload := aggregatedModelListResponse{
		Object: "list",
		Data:   make([]aggregatedModelListEntry, 0, len(models)),
	}
	for _, model := range models {
		payload.Data = append(payload.Data, aggregatedModelListEntry{
			ID:      model,
			Object:  "model",
			OwnedBy: "llm-tracelab",
		})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "failed to marshal model list", http.StatusInternalServerError)
		return
	}

	logInfo, err := h.recorder.PrepareLogFileWithOptions(r, recorder.PrepareOptions{
		RoutingPolicy: h.routerPolicy(),
	})
	if err != nil {
		slog.Error("Failed to prepare aggregated model-list log file", "err", err)
		http.Error(w, "Internal Logging Error", http.StatusInternalServerError)
		return
	}
	logInfo.Events = append(logInfo.Events, recorder.RecordEvent{
		Type: "routing.aggregate",
		Time: start,
		Attributes: map[string]interface{}{
			"endpoint":    "/v1/models",
			"model_count": len(models),
		},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		slog.Error("Failed to write aggregated model-list response", "err", err)
	}

	headerBuf := bytes.NewBufferString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", http.StatusOK, http.StatusText(http.StatusOK)))
	headerBuf.WriteString("Content-Type: application/json\r\n")
	headerBuf.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(body)))
	headerBuf.WriteString("\r\n")
	if _, err := logInfo.File.Write([]byte("\n")); err != nil {
		slog.Error("Failed to write aggregated model-list separator", "path", logInfo.Path, "err", err)
		_ = logInfo.File.Close()
		return
	}
	nHead, err := logInfo.File.Write(headerBuf.Bytes())
	if err != nil {
		slog.Error("Failed to write aggregated model-list response header", "path", logInfo.Path, "err", err)
		_ = logInfo.File.Close()
		return
	}
	nBody, err := logInfo.File.Write(body)
	if err != nil {
		slog.Error("Failed to write aggregated model-list response body", "path", logInfo.Path, "err", err)
		_ = logInfo.File.Close()
		return
	}

	logInfo.Header.Meta.DurationMs = time.Since(start).Milliseconds()
	logInfo.Header.Meta.StatusCode = http.StatusOK
	logInfo.Header.Meta.ContentLength = int64(len(body))
	logInfo.Header.Layout.ResHeaderLen = int64(nHead)
	logInfo.Header.Layout.ResBodyLen = int64(nBody)
	logInfo.Header.Layout.IsStream = false
	if err := h.recorder.UpdateLogFile(logInfo); err != nil {
		slog.Error("Failed to update aggregated model-list log file", "path", logInfo.Path, "err", err)
	}
}

func (h *Handler) routerPolicy() string {
	if h == nil || h.router == nil {
		return ""
	}
	return h.router.Policy()
}

func (h *Handler) recordSelectionFailure(r *http.Request, start time.Time, statusCode int, selectErr error) {
	h.recordSelectionFailureWithBody(r, start, statusCode, selectErr, nil)
}

func (h *Handler) recordSelectionFailureWithBody(r *http.Request, start time.Time, statusCode int, selectErr error, bodyBytes []byte) {
	if h == nil || h.recorder == nil || r == nil {
		return
	}
	reason := router.SelectionFailureReason(selectErr)
	logInfo, err := h.recorder.PrepareLogFileWithOptionsAndBody(r, recorder.PrepareOptions{
		RoutingPolicy:        h.routerPolicy(),
		RoutingFailureReason: reason,
	}, bodyBytes)
	if err != nil {
		slog.Error("Failed to prepare selection-failure log file", "err", err)
		return
	}

	body := []byte("Proxy Error: " + selectErr.Error() + "\n")
	headerBuf := bytes.NewBufferString(fmt.Sprintf("HTTP/1.1 %d %s\r\n", statusCode, http.StatusText(statusCode)))
	headerBuf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	headerBuf.WriteString("X-Content-Type-Options: nosniff\r\n")
	headerBuf.WriteString(fmt.Sprintf("Content-Length: %d\r\n", len(body)))
	headerBuf.WriteString("\r\n")

	if _, err := logInfo.File.Write([]byte("\n")); err != nil {
		slog.Error("Failed to write selection-failure separator", "path", logInfo.Path, "err", err)
		_ = logInfo.File.Close()
		return
	}
	nHead, err := logInfo.File.Write(headerBuf.Bytes())
	if err != nil {
		slog.Error("Failed to write selection-failure response header", "path", logInfo.Path, "err", err)
		_ = logInfo.File.Close()
		return
	}
	nBody, err := logInfo.File.Write(body)
	if err != nil {
		slog.Error("Failed to write selection-failure response body", "path", logInfo.Path, "err", err)
		_ = logInfo.File.Close()
		return
	}

	logInfo.Header.Meta.Error = selectErr.Error()
	logInfo.Header.Meta.StatusCode = statusCode
	logInfo.Header.Meta.DurationMs = time.Since(start).Milliseconds()
	logInfo.Header.Meta.ContentLength = int64(len(body))
	logInfo.Header.Layout.ResHeaderLen = int64(nHead)
	logInfo.Header.Layout.ResBodyLen = int64(nBody)
	logInfo.Events = append(logInfo.Events, recorder.RecordEvent{
		Type:    "routing.failure",
		Time:    time.Now().UTC(),
		Message: selectErr.Error(),
		Attributes: map[string]interface{}{
			"routing_policy":         h.routerPolicy(),
			"routing_failure_reason": reason,
			"http_status":            statusCode,
		},
	})

	if err := h.recorder.UpdateLogFile(logInfo); err != nil {
		slog.Error("Failed to update selection-failure log file", "path", logInfo.Path, "err", err)
	}
}
