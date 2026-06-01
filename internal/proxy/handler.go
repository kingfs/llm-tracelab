package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/auth"
	"github.com/kingfs/llm-tracelab/internal/chaos"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/recorder"
	"github.com/kingfs/llm-tracelab/internal/redaction"
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

const (
	upstreamRetryBudget       = 30 * time.Second
	upstreamRetryWaitCapacity = 16
)

type retrySleeper func(context.Context, time.Duration) bool

var sleepForRetry retrySleeper = defaultSleepForRetry

var upstreamRetryWaitSlots = make(chan struct{}, upstreamRetryWaitCapacity)

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
			*s.Events = append(*s.Events, events...)
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
		lastErr       error
		logInfo       *recorder.LogInfo
		selection     *router.Selection
		triedIDs      []string
		retryAttempt  int
		retryDeadline = start.Add(upstreamRetryBudget)
		retryEvents   []recorder.RecordEvent
		waitSlotHeld  bool
	)
	defer func() {
		if waitSlotHeld {
			releaseRetryWaitSlot()
		}
	}()

	// 重试循环：优先快速尝试候选上游目标；所有候选短时不可用时，在
	// 30s 预算内做指数退避后重新选择，避免把短暂上游抖动直接暴露成 502。
	for {
		var selErr error
		if len(triedIDs) == 0 {
			selection, selErr = h.router.SelectWithBody(r, bodyBytes)
		} else {
			selection, selErr = h.router.SelectWithExclusion(r, bodyBytes, triedIDs)
		}
		if selErr != nil {
			if router.SelectionFailureReason(selErr) == router.SelectionFailureAllTargetsOpen && time.Now().Before(retryDeadline) {
				if !waitSlotHeld {
					if !tryAcquireRetryWaitSlot() {
						retryEvents = append(retryEvents, retryEvent("routing.retry_queue_saturated", retryAttempt, 0, "", 0, "all_targets_open", false))
						slog.Warn("Upstream retry wait queue saturated")
						h.recordSelectionFailureWithBody(r, start, http.StatusServiceUnavailable, selErr, bodyBytes, retryEvents)
						http.Error(w, "Proxy overloaded: upstream retry wait queue saturated", http.StatusServiceUnavailable)
						return
					}
					waitSlotHeld = true
				}
				delay := retryDelay(retryAttempt, nil, retryDeadline)
				retryEvents = append(retryEvents, retryEvent("routing.retry_wait", retryAttempt, delay, "", 0, "all_targets_open", true))
				if !sleepBeforeRetry(r.Context(), delay) {
					if r.Context().Err() != nil {
						return
					}
				} else {
					retryAttempt++
					triedIDs = nil
					_, _ = h.router.RefreshNow()
					retryEvents = append(retryEvents, retryEvent("routing.refresh", retryAttempt, 0, "", 0, "all_targets_open", true))
					continue
				}
			}
			slog.Error("Failed to select upstream target", "error", selErr)
			// 如果是第一次就失败，保持原有的 selection-failure 记录行为。
			if len(triedIDs) == 0 {
				h.recordSelectionFailureWithBody(r, start, http.StatusBadGateway, selErr, bodyBytes, retryEvents)
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
		logInfo.Events = append(logInfo.Events, routingDecisionEvents(selection.Decision, start)...)

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
			retryEvents = append(retryEvents, retryEvent("routing.retry_candidate", retryAttempt, 0, selection.Target.ID, 0, reqErr.Error(), false))
			h.closeLogFile(logInfo)
			if len(triedIDs) >= selection.CandidateCount {
				delay := retryDelay(retryAttempt, nil, retryDeadline)
				retryEvents = append(retryEvents, retryEvent("routing.retry_wait", retryAttempt, delay, selection.Target.ID, 0, reqErr.Error(), true))
				if !sleepBeforeRetry(r.Context(), delay) {
					if r.Context().Err() != nil {
						return
					}
					break
				}
				retryAttempt++
				triedIDs = nil
				_, _ = h.router.RefreshNow()
				retryEvents = append(retryEvents, retryEvent("routing.refresh", retryAttempt, 0, selection.Target.ID, 0, reqErr.Error(), true))
			}
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
			retryEvents = append(retryEvents, retryEvent("routing.retry_candidate", retryAttempt, 0, selection.Target.ID, resp.StatusCode, logInfo.Header.Meta.Error, false))
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
			h.closeLogFile(logInfo)
			if len(triedIDs) >= selection.CandidateCount {
				if !isTransientRetryStatus(resp.StatusCode) {
					break
				}
				delay := retryDelay(retryAttempt, retryAfter, retryDeadline)
				retryEvents = append(retryEvents, retryEvent("routing.retry_wait", retryAttempt, delay, selection.Target.ID, resp.StatusCode, logInfo.Header.Meta.Error, true))
				if !sleepBeforeRetry(r.Context(), delay) {
					if r.Context().Err() != nil {
						return
					}
					break
				}
				retryAttempt++
				triedIDs = nil
				_, _ = h.router.RefreshNow()
				retryEvents = append(retryEvents, retryEvent("routing.refresh", retryAttempt, 0, selection.Target.ID, resp.StatusCode, logInfo.Header.Meta.Error, true))
			}
			continue
		}

		// 成功 —— 将上游响应写入客户端
		h.writeUpstreamResponse(irw, resp, logInfo, selection, start, r, retryEvents)
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
		h.recordSelectionFailureWithBody(r, start, http.StatusBadGateway, lastErr, bodyBytes, retryEvents)
	}
	http.Error(w, "Proxy Error: "+lastErr.Error(), http.StatusBadGateway)
}

func sleepBeforeRetry(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return false
	}
	return sleepForRetry(ctx, delay)
}

func defaultSleepForRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func retryDelay(attempt int, retryAfter *time.Duration, deadline time.Time) time.Duration {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0
	}
	delay := retryBackoffWithJitter(attempt)
	if retryAfter != nil && *retryAfter > delay {
		delay = *retryAfter
	}
	if delay > remaining {
		delay = remaining
	}
	return delay
}

func retryBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := 250 * time.Millisecond
	for i := 0; i < attempt && delay < 5*time.Second; i++ {
		delay *= 2
	}
	if delay > 5*time.Second {
		return 5 * time.Second
	}
	return delay
}

func retryBackoffWithJitter(attempt int) time.Duration {
	base := retryBackoff(attempt)
	if base <= 0 {
		return 0
	}
	// 20% positive jitter prevents coordinated wakeups while preserving the
	// minimum backoff expected by tests and operators.
	jitter := time.Duration(rand.Int63n(int64(base/5) + 1))
	return base + jitter
}

func parseRetryAfter(raw string, now time.Time) *time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			zero := time.Duration(0)
			return &zero
		}
		delay := time.Duration(seconds) * time.Second
		return &delay
	}
	when, err := http.ParseTime(raw)
	if err != nil {
		return nil
	}
	delay := when.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return &delay
}

func retryEvent(eventType string, attempt int, delay time.Duration, upstreamID string, statusCode int, reason string, refresh bool) recorder.RecordEvent {
	attrs := map[string]interface{}{
		"attempt":      attempt,
		"delay_ms":     delay.Milliseconds(),
		"refresh_next": refresh,
	}
	if upstreamID != "" {
		attrs["upstream_id"] = upstreamID
	}
	if statusCode > 0 {
		attrs["status_code"] = statusCode
	}
	if reason != "" {
		attrs["reason"] = reason
	}
	return recorder.RecordEvent{
		Type:       eventType,
		Time:       time.Now().UTC(),
		Attributes: attrs,
	}
}

func routingDecisionEvents(decision *router.DecisionTrace, eventTime time.Time) []recorder.RecordEvent {
	if decision == nil {
		return nil
	}
	events := []recorder.RecordEvent{
		{
			Type: "routing.classified",
			Time: eventTime,
			Attributes: map[string]interface{}{
				"model":           decision.ModelName,
				"endpoint":        decision.Endpoint,
				"routing_policy":  decision.Policy,
				"fallback_policy": decision.FallbackPolicy,
				"excluded_ids":    decision.ExcludedIDs,
			},
		},
		{
			Type: "routing.candidates",
			Time: eventTime,
			Attributes: map[string]interface{}{
				"available_count": len(selectableCandidateIDs(decision.Candidates)),
				"candidates":      candidateEventAttributes(decision.Candidates),
			},
		},
	}
	if decision.SelectedID != "" {
		events = append(events, recorder.RecordEvent{
			Type: "routing.selected",
			Time: eventTime,
			Attributes: map[string]interface{}{
				"upstream_id":   decision.SelectedID,
				"routing_score": decision.SelectedScore,
			},
		})
	}
	for _, sticky := range decision.StickyEvents {
		if sticky.Status == "" {
			continue
		}
		attrs := map[string]interface{}{
			"sticky_status": sticky.Status,
		}
		if sticky.Key != "" {
			attrs["sticky_key_fingerprint"] = stickyKeyFingerprint(sticky.Key)
		}
		if sticky.TargetID != "" {
			attrs["upstream_id"] = sticky.TargetID
		}
		if sticky.BreakID != "" {
			attrs["previous_upstream_id"] = sticky.BreakID
		}
		events = append(events, recorder.RecordEvent{
			Type:       "routing.sticky." + sticky.Status,
			Time:       eventTime,
			Attributes: attrs,
		})
	}
	if decision.FailureReason != "" {
		events = append(events, recorder.RecordEvent{
			Type: "routing.filtered",
			Time: eventTime,
			Attributes: map[string]interface{}{
				"routing_failure_reason": decision.FailureReason,
				"filtered_count":         filteredCandidateCount(decision.Candidates),
			},
		})
	}
	return events
}

func stickyKeyFingerprint(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("sha256:%x", sum[:8])
}

func routingOutcomeEvent(selection *router.Selection, statusCode int, duration time.Duration, errText string) recorder.RecordEvent {
	attrs := map[string]interface{}{
		"status_code": statusCode,
		"duration_ms": duration.Milliseconds(),
	}
	if selection != nil && selection.Target != nil {
		attrs["upstream_id"] = selection.Target.ID
		attrs["model"] = selection.Request.ModelName
	}
	if errText != "" {
		attrs["error"] = errText
	}
	return recorder.RecordEvent{
		Type:       "routing.outcome",
		Time:       time.Now().UTC(),
		Attributes: attrs,
	}
}

func candidateEventAttributes(candidates []router.CandidateDecision) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(candidates))
	for _, candidate := range candidates {
		attrs := map[string]interface{}{
			"id":              candidate.ID,
			"provider_preset": candidate.ProviderPreset,
			"priority":        candidate.Priority,
			"weight":          candidate.Weight,
			"health_state":    candidate.HealthState,
			"supports_path":   candidate.SupportsPath,
			"supports_model":  candidate.SupportsModel,
			"selectable":      candidate.Selectable,
		}
		if candidate.BaseURL != "" {
			attrs["base_url"] = redaction.DisplayURL(candidate.BaseURL)
		}
		if candidate.Excluded {
			attrs["excluded"] = true
		}
		if candidate.FilterReason != "" {
			attrs["filter_reason"] = candidate.FilterReason
		}
		out = append(out, attrs)
	}
	return out
}

func selectableCandidateIDs(candidates []router.CandidateDecision) []string {
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Selectable {
			out = append(out, candidate.ID)
		}
	}
	return out
}

func filteredCandidateCount(candidates []router.CandidateDecision) int {
	count := 0
	for _, candidate := range candidates {
		if !candidate.Selectable {
			count++
		}
	}
	return count
}

func tryAcquireRetryWaitSlot() bool {
	select {
	case upstreamRetryWaitSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func releaseRetryWaitSlot() {
	select {
	case <-upstreamRetryWaitSlots:
	default:
	}
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
	retryEvents []recorder.RecordEvent,
) {
	logInfo.Events = append(logInfo.Events, retryEvents...)
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
	logInfo.Events = append(logInfo.Events, routingOutcomeEvent(selection, code, duration, logInfo.Header.Meta.Error))

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

func isTransientRetryStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
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

func (h *Handler) recordSelectionFailureWithBody(r *http.Request, start time.Time, statusCode int, selectErr error, bodyBytes []byte, retryEvents []recorder.RecordEvent) {
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
	logInfo.Events = append(logInfo.Events, retryEvents...)
	if decision := router.SelectionDecision(selectErr); decision != nil {
		logInfo.Events = append(logInfo.Events, routingDecisionEvents(decision, start)...)
	}
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
