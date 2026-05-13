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
	"os"
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

	irw := NewInstrumentedResponseWriter(w)

	var (
		lastErr   error
		logInfo   *recorder.LogInfo
		selection *router.Selection
		triedIDs  []string
	)

	// 重试循环：逐个尝试候选上游目标，遇到可重试失败时自动降级到下一个。
	for {
		var selErr error
		if len(triedIDs) == 0 {
			selection, selErr = h.router.SelectWithBody(r, bodyBytes)
		} else {
			selection, selErr = h.router.SelectWithExclusion(r, bodyBytes, triedIDs)
		}
		if selErr != nil {
			slog.Error("Failed to select upstream target", "error", selErr)
			// 如果是第一次就失败，保持原有的 selection-failure 记录行为。
			if len(triedIDs) == 0 {
				h.recordSelectionFailureWithBody(r, start, http.StatusBadGateway, selErr, bodyBytes)
				http.Error(w, selErr.Error(), http.StatusBadGateway)
				return
			}
			lastErr = selErr
			break
		}
		triedIDs = append(triedIDs, selection.Target.ID)

		// 准备日志
		logInfo, err = h.recorder.PrepareLogFileWithOptionsAndBody(r, recorder.PrepareOptions{
			SiteURL:                        selection.Target.Upstream.BaseURL,
			SelectedUpstreamID:             selection.Target.ID,
			SelectedUpstreamProviderPreset: selection.Target.Upstream.ProviderPreset,
			RoutingPolicy:                  h.routerPolicy(),
			RoutingScore:                   selection.Score,
			RoutingCandidateCount:          selection.CandidateCount,
		}, bodyBytes)
		if err != nil {
			slog.Error("Failed to prepare log file", "err", err)
			h.router.Complete(selection, router.Outcome{
				Success:    false,
				StatusCode: http.StatusInternalServerError,
				Stream:     selection.Request.Stream,
			})
			lastErr = err
			break
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

		// Chaos
		chaosRes := h.chaosManager.Evaluate(logInfo.Header.Meta.Model)
		if chaosRes.ShouldInject {
			if chaosRes.Action == "delay" {
				time.Sleep(chaosRes.Delay)
			}
		}

		// 发送请求到上游
		resp, reqErr := h.sendUpstreamRequest(r, selection.Target, bodyBytes)
		if reqErr != nil {
			// 网络层面错误（TCP 连接失败、TLS 握手失败、超时等）→ 可重试
			logInfo.Header.Meta.Error = reqErr.Error()
			slog.Warn("Upstream request failed, will retry with next candidate",
				"upstream_id", selection.Target.ID,
				"model", selection.Request.ModelName,
				"error", reqErr,
			)
			lastErr = reqErr
			h.router.Complete(selection, router.Outcome{
				Success:    false,
				StatusCode: 0,
				Stream:     selection.Request.Stream,
			})
			h.closeLogFile(logInfo)
			continue
		}

		if isRetryableStatus(resp.StatusCode) {
			resp.Body.Close()
			logInfo.Header.Meta.Error = fmt.Sprintf("upstream returned status %d", resp.StatusCode)
			slog.Warn("Upstream returned retryable status, will retry with next candidate",
				"upstream_id", selection.Target.ID,
				"model", selection.Request.ModelName,
				"status", resp.StatusCode,
			)
			lastErr = fmt.Errorf("upstream %s returned status %d for model %q", selection.Target.ID, resp.StatusCode, selection.Request.ModelName)
			h.router.Complete(selection, router.Outcome{
				Success:    false,
				StatusCode: resp.StatusCode,
				DurationMs: float64(time.Since(start).Milliseconds()),
				Stream:     selection.Request.Stream,
			})
			h.closeLogFile(logInfo)
			if len(triedIDs) >= selection.CandidateCount {
				break
			}
			continue
		}

		// 成功 —— 将上游响应写入客户端
		h.writeUpstreamResponse(irw, resp, logInfo, selection, start, r)
		return
	}

	// 全部候选目标都已尝试且失败
	modelName := ""
	if selection != nil {
		modelName = selection.Request.ModelName
	}
	slog.Error("All upstream targets exhausted",
		"model", modelName,
		"tried", triedIDs,
		"last_error", lastErr,
	)
	// Record the failure with a fresh log file (previous attempt logs were already
	// closed by closeLogFile in the retry loop).
	if h.recorder != nil {
		h.recordSelectionFailureWithBody(r, start, http.StatusBadGateway, lastErr, bodyBytes)
	}
	http.Error(w, "Proxy Error: "+lastErr.Error(), http.StatusBadGateway)
}

// sendUpstreamRequest prepares a request targeting the given upstream and executes it.
// It creates a fresh outgoing request (not cloning the original, because http.Request.Clone
// discards the Body), applies the director logic (URL rewrite, auth headers), and returns
// the upstream response. The caller is responsible for closing resp.Body.
func (h *Handler) sendUpstreamRequest(original *http.Request, target *router.Target, bodyBytes []byte) (*http.Response, error) {
	clientPath := original.URL.Path
	if original.URL.RawQuery != "" {
		clientPath += "?" + original.URL.RawQuery
	}

	fullURL, err := target.Upstream.BuildURL(clientPath)
	if err != nil {
		return nil, fmt.Errorf("build target URL: %w", err)
	}

	var body io.Reader
	if bodyBytes != nil {
		body = bytes.NewReader(bodyBytes)
	}
	outreq, err := http.NewRequestWithContext(context.Background(), original.Method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("create outbound request: %w", err)
	}

	// Copy relevant headers from the original request.
	for key, vals := range original.Header {
		for _, val := range vals {
			outreq.Header.Add(key, val)
		}
	}

	target.Upstream.ApplyAuthHeaders(outreq.Header)
	outreq.Header.Set("Accept-Encoding", "identity")

	if bodyBytes != nil {
		outreq.ContentLength = int64(len(bodyBytes))
	}

	return h.proxy.Transport.RoundTrip(outreq)
}

// writeUpstreamResponse writes a successful upstream response to the client response writer,
// recording metrics and usage along the way (equivalent to the old ModifyResponse + defer block).
func (h *Handler) writeUpstreamResponse(
	irw *InstrumentedResponseWriter,
	resp *http.Response,
	logInfo *recorder.LogInfo,
	selection *router.Selection,
	start time.Time,
	originalReq *http.Request,
) {
	// Write separator and response header to log file.
	logInfo.File.Write([]byte("\n"))
	headerBuf := bytes.NewBufferString(fmt.Sprintf("%s %s\r\n", resp.Proto, resp.Status))
	resp.Header.Write(headerBuf)
	headerBuf.WriteString("\r\n")
	n, _ := logInfo.File.Write(headerBuf.Bytes())
	logInfo.Header.Layout.ResHeaderLen = int64(n)

	// Detect streaming mode.
	isStream := llm.DetectStreamingResponse(resp.Header)
	logInfo.Header.Layout.IsStream = isStream

	// Wrap response body with UsageSniffer.
	resp.Body = &UsageSniffer{
		Source:   resp.Body,
		File:     logInfo.File,
		Count:    &logInfo.Header.Layout.ResBodyLen,
		Usage:    &logInfo.Header.Usage,
		Pipeline: llm.NewResponsePipeline(logInfo.Header.Meta.Provider, logInfo.Header.Meta.Endpoint, isStream),
		Events:   &logInfo.Events,
	}

	// Copy response headers to client.
	for key, vals := range resp.Header {
		for _, val := range vals {
			irw.Header().Add(key, val)
		}
	}

	// Write status code then body.
	irw.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(irw, resp.Body); err != nil && err != io.EOF {
		logInfo.Header.Meta.Error = "failed to copy response body: " + err.Error()
	}

	// Close the sniffer so Finalize() runs before we call UpdateLogFile.
	resp.Body.Close()

	// Finalize metrics and log (equivalent to old defer block).
	duration := time.Since(start)
	code, written, ttft := irw.GetMetrics()

	logInfo.Header.Meta.DurationMs = duration.Milliseconds()
	logInfo.Header.Meta.StatusCode = code
	logInfo.Header.Meta.ContentLength = written
	logInfo.Header.Meta.TTFTMs = ttft

	if uErr := h.recorder.UpdateLogFile(logInfo); uErr != nil {
		slog.Error("Failed to update log file", "path", logInfo.Path, "err", uErr)
	}
	h.router.Complete(selection, router.Outcome{
		Success:        code >= 200 && code < 300 && logInfo.Header.Meta.Error == "",
		ClientCanceled: originalReq.Context().Err() != nil,
		StatusCode:     code,
		DurationMs:     float64(duration.Milliseconds()),
		TTFTMs:         float64(ttft),
		Stream:         logInfo.Header.Layout.IsStream || selection.Request.Stream,
	})

	slog.Info("Request completed",
		"model", logInfo.Header.Meta.Model,
		"selected_upstream_id", logInfo.Header.Meta.SelectedUpstreamID,
		"status", code,
		"tokens_total", logInfo.Header.Usage.TotalTokens,
	)
}

// closeLogFile closes and removes the log file for a failed attempt so stale
// recordings from retried targets do not interfere with later lookup.
func (h *Handler) closeLogFile(logInfo *recorder.LogInfo) {
	if logInfo == nil || logInfo.File == nil {
		return
	}
	_ = logInfo.File.Close()
	if logInfo.Path != "" {
		_ = os.Remove(logInfo.Path)
	}
}

// isRetryableStatus returns true for HTTP status codes that indicate the upstream may be
// temporarily unable to serve this specific model but another upstream might succeed.
func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusNotFound: // 404 — model not available on this upstream
		return true
	case http.StatusTooManyRequests: // 429 — rate limited on this upstream
		return true
	case http.StatusInternalServerError, // 500
		http.StatusBadGateway,         // 502
		http.StatusServiceUnavailable, // 503
		http.StatusGatewayTimeout:     // 504
		return true
	default:
		return code >= http.StatusInternalServerError
	}
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
	fmt.Fprintf(headerBuf, "Content-Length: %d\r\n", len(body))
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
	fmt.Fprintf(headerBuf, "Content-Length: %d\r\n", len(body))
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
