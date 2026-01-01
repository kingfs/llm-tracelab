package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/chaos"
	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/recorder"
)

// ensureStreamOptions 检查请求体，如果是 stream 模式，强制注入 stream_options
func ensureStreamOptions(req *http.Request) {
	// 1. 只有 POST 请求且 Content-Type 为 JSON 才处理
	if req.Method != http.MethodPost || !strings.Contains(req.Header.Get("Content-Type"), "application/json") {
		return
	}

	// 2. 读取 Body
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return // 读失败，放弃修改
	}
	// 无论是否修改，最后都要还原 Body
	defer func() {
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}()

	// 3. 解析 JSON
	// 使用 map[string]interface{} 以保留原始结构
	var payload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return // 不是 JSON，放弃
	}

	// 4. 检查 stream 字段
	isStream, ok := payload["stream"].(bool)
	if !ok || !isStream {
		return // 不是 stream 模式，放弃
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
			bodyBytes = newBytes // 更新外部的 bodyBytes，供 defer 还原使用
			req.ContentLength = int64(len(newBytes))
			req.Header.Set("Content-Length", fmt.Sprint(len(newBytes)))
			// slog.Debug("Injected stream_options: include_usage=true")
		}
	}
}

// UsageSniffer 纯粹的嗅探器，不再做估算
type UsageSniffer struct {
	Source   io.ReadCloser
	File     io.Writer
	Count    *int64
	Usage    *recorder.UsageInfo
	IsStream bool

	// Stream 模式专用：行缓冲区
	lineBuf []byte
	// Non-Stream 模式专用：尾部滑动窗口
	tailBuf []byte
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

		// 2. 嗅探 Usage
		if s.IsStream {
			s.sniffStream(data)
		} else {
			s.sniffNonStream(data)
		}
	}
	return
}

// sniffStream 针对 SSE 流式数据的逐行解析
func (s *UsageSniffer) sniffStream(chunk []byte) {
	s.lineBuf = append(s.lineBuf, chunk...)

	// 限制缓冲区大小
	if len(s.lineBuf) > 64*1024 {
		copy(s.lineBuf, s.lineBuf[len(s.lineBuf)-64*1024:])
		s.lineBuf = s.lineBuf[:64*1024]
	}

	for {
		idx := bytes.IndexByte(s.lineBuf, '\n')
		if idx == -1 {
			break
		}

		line := s.lineBuf[:idx]
		s.lineBuf = s.lineBuf[idx+1:]

		// 快速过滤
		if !bytes.Contains(line, []byte(`"usage"`)) {
			continue
		}

		lineStr := string(line)
		// 处理 data: 前缀
		if strings.HasPrefix(lineStr, "data:") {
			jsonStr := strings.TrimSpace(strings.TrimPrefix(lineStr, "data:"))
			// 尝试解析
			var chunkObj struct {
				Usage *recorder.UsageInfo `json:"usage"`
			}
			if json.Unmarshal([]byte(jsonStr), &chunkObj) == nil {
				if chunkObj.Usage != nil && chunkObj.Usage.TotalTokens > 0 {
					*s.Usage = *chunkObj.Usage
				}
			}
		}
	}
}

// sniffNonStream 针对普通 JSON 的尾部缓冲解析
func (s *UsageSniffer) sniffNonStream(chunk []byte) {
	maxBuf := 4096
	if len(s.tailBuf)+len(chunk) > maxBuf {
		combined := append(s.tailBuf, chunk...)
		start := len(combined) - maxBuf
		if start < 0 {
			start = 0
		}
		s.tailBuf = combined[start:]
	} else {
		s.tailBuf = append(s.tailBuf, chunk...)
	}
}

func (s *UsageSniffer) Close() error {
	// Non-Stream 模式在结束时解析
	if !s.IsStream && s.Usage != nil && s.Usage.TotalTokens == 0 {
		extractUsageFromTail(s.tailBuf, s.Usage)
	}
	return s.Source.Close()
}

// extractUsageFromTail 从数据末尾提取 usage (Non-Stream)
func extractUsageFromTail(data []byte, target *recorder.UsageInfo) {
	if len(data) == 0 {
		return
	}
	str := string(data)

	idx := strings.LastIndex(str, `"usage"`)
	if idx == -1 {
		return
	}

	segment := str[idx:]
	startBrace := strings.Index(segment, "{")
	if startBrace == -1 {
		return
	}

	jsonPart := segment[startBrace:]
	depth := 0
	endBrace := -1
	for i, r := range jsonPart {
		if r == '{' {
			depth++
		} else if r == '}' {
			depth--
			if depth == 0 {
				endBrace = i + 1
				break
			}
		}
	}

	if endBrace != -1 {
		jsonStr := jsonPart[:endBrace]
		var u recorder.UsageInfo
		if json.Unmarshal([]byte(jsonStr), &u) == nil {
			if u.TotalTokens > 0 {
				*target = u
			}
		}
	}
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
}

func NewHandler(cfg *config.Config) (*Handler, error) {
	targetURL, err := url.Parse(cfg.Upstream.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream url: %w", err)
	}

	rec := recorder.New(cfg.Debug.OutputDir, cfg.Debug.MaskKey)
	cm := chaos.New(cfg)

	rp := httputil.NewSingleHostReverseProxy(targetURL)
	rp.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	originalDirector := rp.Director
	rp.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = targetURL.Host
		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		req.Header.Set("Authorization", "Bearer "+cfg.Upstream.ApiKey)
		req.Header.Set("Accept-Encoding", "identity")
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		logInfo, ok := resp.Request.Context().Value("LogInfo").(*recorder.LogInfo)
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
		isStream := false
		if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") || resp.Header.Get("Transfer-Encoding") == "chunked" {
			isStream = true
			logInfo.Header.Layout.IsStream = true
		}

		// 4. 劫持 Body
		resp.Body = &UsageSniffer{
			Source:   resp.Body,
			File:     logInfo.File,
			Count:    &logInfo.Header.Layout.ResBodyLen,
			Usage:    &logInfo.Header.Usage,
			IsStream: isStream,
		}
		return nil
	}

	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Error("Proxy error", "err", err)
		if logInfo, ok := r.Context().Value("LogInfo").(*recorder.LogInfo); ok && logInfo != nil {
			logInfo.File.Close()
		}
		http.Error(w, "Proxy Error: "+err.Error(), http.StatusBadGateway)
	}

	return &Handler{
		proxy:        rp,
		recorder:     rec,
		chaosManager: cm,
		cfg:          cfg,
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// [Step 1] 自动注入 stream_options
	ensureStreamOptions(r)

	// [Step 2] 准备日志 (此时 r.Body 已经包含 injected options，Log 里会记录下来)
	logInfo, err := h.recorder.PrepareLogFile(r, h.cfg.Upstream.BaseURL)
	if err != nil {
		slog.Error("Failed to prepare log file", "err", err)
		http.Error(w, "Internal Logging Error", 500)
		return
	}

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

		slog.Info("Request completed",
			"model", logInfo.Header.Meta.Model,
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

	ctx := context.WithValue(r.Context(), "LogInfo", logInfo)
	h.proxy.ServeHTTP(irw, r.WithContext(ctx))
}
