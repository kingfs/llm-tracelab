package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
	"github.com/kingfs/llm-tracelab/internal/router"
	"github.com/kingfs/llm-tracelab/internal/store"
)

// This file pins down the downstream-entrypoint x upstream-protocol matrix.
//
// TraceLab accepts three client surfaces (/v1/chat/completions, /v1/responses,
// /v1/messages) and forwards to upstreams that speak one of those protocols.
// The hot path deliberately does NOT translate schemas across protocol
// families, with one exception: when the local Responses server is enabled, a
// /v1/responses request can be executed against a Chat Completions upstream by
// running the local Responses runtime and calling upstream
// /v1/chat/completions.
//
// The matrix therefore is:
//
//	entrypoint     upstream        outcome
//	chat           chat            200 proxy_pass
//	chat           responses       502 (no cross-protocol translation)
//	chat           messages        502 (no cross-protocol translation)
//	responses      chat            200 responses_server (local translation)
//	responses      responses       200 proxy_pass
//	responses      messages        502 (no cross-protocol translation)
//	messages       messages        200 proxy_pass
//	messages       chat            502 (no cross-protocol translation)
//	messages       responses       502 (no cross-protocol translation)
//
// Anything that cannot be served must fail loudly (502 with zero upstream
// calls) instead of silently forwarding a request to an upstream that cannot
// understand it.

const matrixModel = "matrix-model"

type protocolKind string

const (
	protocolChatCompletions protocolKind = "chat_completions"
	protocolResponses       protocolKind = "responses"
	protocolAnthropicMsgs   protocolKind = "anthropic_messages"
)

// clientPath is the downstream path a client uses for this protocol.
func (k protocolKind) clientPath() string {
	switch k {
	case protocolChatCompletions:
		return "/v1/chat/completions"
	case protocolResponses:
		return "/v1/responses"
	case protocolAnthropicMsgs:
		return "/v1/messages"
	default:
		return ""
	}
}

// matrixUpstream is a fake upstream that records what it received and replies
// with a canonical body for its protocol.
type matrixUpstream struct {
	kind   protocolKind
	server *httptest.Server
	calls  atomic.Int32

	mu     sync.Mutex
	path   string
	body   string
	stream bool
	auth   string
	apiKey string
}

func newMatrixUpstream(t *testing.T, kind protocolKind) *matrixUpstream {
	t.Helper()
	u := &matrixUpstream{kind: kind}
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		isStream, _ := payload["stream"].(bool)

		u.mu.Lock()
		u.path = r.URL.Path
		u.body = string(raw)
		u.stream = isStream
		u.auth = r.Header.Get("Authorization")
		u.apiKey = r.Header.Get("x-api-key")
		u.mu.Unlock()

		if isStream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, matrixStreamBody(kind))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, matrixJSONBody(kind))
	}))
	t.Cleanup(u.server.Close)
	return u
}

func (u *matrixUpstream) snapshot() (path, body string, stream bool, auth, apiKey string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.path, u.body, u.stream, u.auth, u.apiKey
}

func (u *matrixUpstream) callCount() int32 { return u.calls.Load() }

// target builds the UpstreamTargetConfig that makes this fake upstream speak
// exactly one protocol surface.
func (u *matrixUpstream) target(id string, priority int, model string) config.UpstreamTargetConfig {
	enabled := true
	target := config.UpstreamTargetConfig{
		ID:             id,
		Enabled:        &enabled,
		Priority:       priority,
		ModelDiscovery: router.ModelDiscoveryStaticOnly,
		StaticModels:   []string{model},
	}
	switch u.kind {
	case protocolChatCompletions:
		target.Upstream = config.UpstreamConfig{
			BaseURL:        u.server.URL + "/v1",
			ApiKey:         "matrix-secret",
			ProviderPreset: "openai",
			APIType:        "chat_completions",
		}
	case protocolResponses:
		target.Upstream = config.UpstreamConfig{
			BaseURL:        u.server.URL + "/v1",
			ApiKey:         "matrix-secret",
			ProviderPreset: "openai",
			APIType:        "responses_native",
			Capabilities: config.UpstreamCapabilitiesConfig{
				Responses:       boolPtr(true),
				ChatCompletions: boolPtr(false),
			},
		}
	case protocolAnthropicMsgs:
		target.Upstream = config.UpstreamConfig{
			BaseURL:        u.server.URL,
			ApiKey:         "matrix-secret",
			ProviderPreset: "anthropic",
			APIType:        "messages",
		}
	}
	return target
}

// matrixJSONBody returns a minimal valid non-streaming response for the kind.
func matrixJSONBody(kind protocolKind) string {
	switch kind {
	case protocolChatCompletions:
		return `{"id":"chatcmpl_matrix","object":"chat.completion","model":"` + matrixModel + `","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	case protocolResponses:
		return `{"id":"resp_matrix","object":"response","status":"completed","model":"` + matrixModel + `","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"pong"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	case protocolAnthropicMsgs:
		return `{"id":"msg_matrix","type":"message","role":"assistant","model":"` + matrixModel + `","content":[{"type":"text","text":"pong"}],"usage":{"input_tokens":1,"output_tokens":2}}`
	default:
		return `{}`
	}
}

// matrixStreamBody returns a minimal SSE body for the kind.
func matrixStreamBody(kind protocolKind) string {
	switch kind {
	case protocolChatCompletions:
		return strings.Join([]string{
			`data: {"id":"chatcmpl_stream_1","object":"chat.completion.chunk","model":"` + matrixModel + `","choices":[{"index":0,"delta":{"role":"assistant","content":"stream "},"finish_reason":null}]}`,
			``,
			`data: {"id":"chatcmpl_stream_1","object":"chat.completion.chunk","model":"` + matrixModel + `","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":"stop"}]}`,
			``,
			`data: {"id":"chatcmpl_stream_1","object":"chat.completion.chunk","model":"` + matrixModel + `","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n")
	case protocolResponses:
		return strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_stream","status":"in_progress"}}`,
			``,
			"event: response.output_text.delta",
			`data: {"type":"response.output_text.delta","delta":"pong"}`,
			``,
			"event: response.completed",
			`data: {"type":"response.completed","response":{"id":"resp_stream","status":"completed"}}`,
			``,
		}, "\n")
	case protocolAnthropicMsgs:
		return strings.Join([]string{
			"event: message_start",
			`data: {"type":"message_start","message":{"id":"msg_stream","type":"message","role":"assistant","model":"` + matrixModel + `","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`,
			``,
			"event: content_block_start",
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			"event: content_block_delta",
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`,
			``,
			"event: content_block_stop",
			`data: {"type":"content_block_stop","index":0}`,
			``,
			"event: message_stop",
			`data: {"type":"message_stop"}`,
			``,
		}, "\n")
	default:
		return ""
	}
}

// matrixRequestBody builds a request the given client entrypoint understands.
func matrixRequestBody(entrypoint protocolKind, model string, stream bool) string {
	streamField := "false"
	if stream {
		streamField = "true"
	}
	switch entrypoint {
	case protocolChatCompletions:
		return fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}],"stream":%s}`, model, streamField)
	case protocolResponses:
		return fmt.Sprintf(`{"model":%q,"input":"ping","stream":%s}`, model, streamField)
	case protocolAnthropicMsgs:
		return fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":[{"type":"text","text":"ping"}]}],"max_tokens":16,"stream":%s}`, model, streamField)
	default:
		return "{}"
	}
}

type matrixResponse struct {
	status      int
	contentType string
	body        string
}

func doMatrixRequest(t *testing.T, proxyURL string, entrypoint protocolKind, model string, stream bool) matrixResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, proxyURL+entrypoint.clientPath(), bytes.NewBufferString(matrixRequestBody(entrypoint, model, stream)))
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return matrixResponse{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), body: string(body)}
}

// newMatrixHandler builds a handler with the given upstream targets.
func newMatrixHandler(t *testing.T, responsesServerEnabled bool, targets ...config.UpstreamTargetConfig) *httptest.Server {
	t.Helper()
	outputDir := t.TempDir()
	st, err := store.New(outputDir)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cfg := &config.Config{
		ResponsesServer: config.ResponsesServerConfig{Enabled: responsesServerEnabled},
		Upstreams:       targets,
	}
	cfg.Debug.OutputDir = outputDir
	cfg.Debug.MaskKey = true

	handler, err := NewHandler(cfg, st)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// TestHandlerDownstreamUpstreamProtocolMatrix exercises every combination of
// the three client entrypoints and the three upstream protocol surfaces with a
// single upstream configured.
func TestHandlerDownstreamUpstreamProtocolMatrix(t *testing.T) {
	tests := []struct {
		name string
		// entrypoint is the client surface the downstream caller uses.
		entrypoint protocolKind
		// upstream is the only configured upstream protocol.
		upstream protocolKind
		// wantStatus is the expected downstream HTTP status.
		wantStatus int
		// wantCalls is how many times the upstream must have been hit.
		wantCalls int32
		// wantUpstreamPath, when set, must match the path the upstream saw.
		wantUpstreamPath string
	}{
		{
			name:             "chat_to_chat_proxies",
			entrypoint:       protocolChatCompletions,
			upstream:         protocolChatCompletions,
			wantStatus:       http.StatusOK,
			wantCalls:        1,
			wantUpstreamPath: "/v1/chat/completions",
		},
		{
			name:       "chat_to_responses_rejected",
			entrypoint: protocolChatCompletions,
			upstream:   protocolResponses,
			wantStatus: http.StatusBadGateway,
			wantCalls:  0,
		},
		{
			name:       "chat_to_messages_rejected",
			entrypoint: protocolChatCompletions,
			upstream:   protocolAnthropicMsgs,
			wantStatus: http.StatusBadGateway,
			wantCalls:  0,
		},
		{
			name:             "responses_to_chat_uses_local_server",
			entrypoint:       protocolResponses,
			upstream:         protocolChatCompletions,
			wantStatus:       http.StatusOK,
			wantCalls:        1,
			wantUpstreamPath: "/v1/chat/completions",
		},
		{
			name:             "responses_to_responses_proxies",
			entrypoint:       protocolResponses,
			upstream:         protocolResponses,
			wantStatus:       http.StatusOK,
			wantCalls:        1,
			wantUpstreamPath: "/v1/responses",
		},
		{
			name:       "responses_to_messages_rejected",
			entrypoint: protocolResponses,
			upstream:   protocolAnthropicMsgs,
			wantStatus: http.StatusBadGateway,
			wantCalls:  0,
		},
		{
			name:             "messages_to_messages_proxies",
			entrypoint:       protocolAnthropicMsgs,
			upstream:         protocolAnthropicMsgs,
			wantStatus:       http.StatusOK,
			wantCalls:        1,
			wantUpstreamPath: "/v1/messages",
		},
		{
			name:       "messages_to_chat_rejected",
			entrypoint: protocolAnthropicMsgs,
			upstream:   protocolChatCompletions,
			wantStatus: http.StatusBadGateway,
			wantCalls:  0,
		},
		{
			name:       "messages_to_responses_rejected",
			entrypoint: protocolAnthropicMsgs,
			upstream:   protocolResponses,
			wantStatus: http.StatusBadGateway,
			wantCalls:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kindUpstream := newMatrixUpstream(t, tt.upstream)
			server := newMatrixHandler(t, true, kindUpstream.target("upstream-"+string(tt.upstream), 100, matrixModel))

			resp := doMatrixRequest(t, server.URL, tt.entrypoint, matrixModel, false)

			if resp.status != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", resp.status, tt.wantStatus, resp.body)
			}
			if got := kindUpstream.callCount(); got != tt.wantCalls {
				t.Fatalf("upstream calls = %d, want %d", got, tt.wantCalls)
			}
			if tt.wantCalls == 0 {
				if !strings.Contains(resp.body, "no upstream target supports") && !strings.Contains(resp.body, "no matching Responses route") {
					t.Fatalf("rejection body = %q, want a routing rejection reason", resp.body)
				}
				return
			}

			// Successful request: the upstream must have seen its own protocol
			// path, the original model, and the configured credentials.
			path, body, _, auth, apiKey := kindUpstream.snapshot()
			if path != tt.wantUpstreamPath {
				t.Fatalf("upstream path = %q, want %q", path, tt.wantUpstreamPath)
			}
			if !strings.Contains(body, matrixModel) {
				t.Fatalf("upstream body = %q, want model %q forwarded", body, matrixModel)
			}
			if tt.upstream == protocolAnthropicMsgs {
				if apiKey != "matrix-secret" {
					t.Fatalf("upstream x-api-key = %q, want matrix-secret", apiKey)
				}
			} else if auth != "Bearer matrix-secret" {
				t.Fatalf("upstream Authorization = %q, want Bearer matrix-secret", auth)
			}
		})
	}
}

// TestHandlerResponsesExecutionModeRecorded checks that the execution mode that
// actually served a /v1/responses request is the one recorded in the cassette,
// for both the native and the local-server paths.
func TestHandlerResponsesExecutionModeRecorded(t *testing.T) {
	tests := []struct {
		name     string
		upstream protocolKind
		wantMode string
	}{
		{name: "native_proxy_pass", upstream: protocolResponses, wantMode: "proxy_pass"},
		{name: "local_responses_server", upstream: protocolChatCompletions, wantMode: "responses_server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputDir := t.TempDir()
			st, err := store.New(outputDir)
			if err != nil {
				t.Fatalf("store.New() error = %v", err)
			}
			defer st.Close()

			upstreamServer := newMatrixUpstream(t, tt.upstream)
			cfg := &config.Config{
				ResponsesServer: config.ResponsesServerConfig{Enabled: true},
				Upstreams: []config.UpstreamTargetConfig{
					upstreamServer.target("only-upstream", 100, matrixModel),
				},
			}
			cfg.Debug.OutputDir = outputDir
			cfg.Debug.MaskKey = true

			handler, err := NewHandler(cfg, st)
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}
			server := httptest.NewServer(handler)
			defer server.Close()

			resp := doMatrixRequest(t, server.URL, protocolResponses, matrixModel, false)
			if resp.status != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", resp.status, resp.body)
			}

			recordPath := waitForRecordedHTTPByEndpoint(t, outputDir, "/v1/responses", time.Second)
			parsed, err := waitForRecordedPrelude(recordPath, time.Second)
			if err != nil {
				t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
			}
			plan := routePlanAttrsFromPrelude(t, parsed)
			if plan["client_entrypoint"] != "/v1/responses" {
				t.Fatalf("route plan client_entrypoint = %v, want /v1/responses", plan["client_entrypoint"])
			}
			if plan["execution_mode"] != tt.wantMode {
				t.Fatalf("route plan execution_mode = %v, want %q", plan["execution_mode"], tt.wantMode)
			}
			if plan["requested_model"] != matrixModel {
				t.Fatalf("route plan requested_model = %v, want %q", plan["requested_model"], matrixModel)
			}
		})
	}
}

// TestHandlerResponsesStrategyUpstreamCombinationMatrix covers the full
// cross-product of responses strategies and available upstream capabilities.
// It is the guardrail for "auto" meaning native-first with local fallback.
func TestHandlerResponsesStrategyUpstreamCombinationMatrix(t *testing.T) {
	type upstreamSet struct {
		native bool
		chat   bool
	}
	sets := []struct {
		name string
		set  upstreamSet
	}{
		{name: "native_only", set: upstreamSet{native: true}},
		{name: "chat_only", set: upstreamSet{chat: true}},
		{name: "native_and_chat", set: upstreamSet{native: true, chat: true}},
	}

	tests := []struct {
		strategy string
		set      string
		// wantStatus defaults to 200 when zero.
		wantStatus     int
		wantNativeCall int32
		wantChatCall   int32
		wantMode       string
	}{
		// auto / prefer_native: native first, local fallback.
		{strategy: "auto", set: "native_only", wantNativeCall: 1, wantMode: "proxy_pass"},
		{strategy: "auto", set: "chat_only", wantChatCall: 1, wantMode: "responses_server"},
		{strategy: "auto", set: "native_and_chat", wantNativeCall: 1, wantMode: "proxy_pass"},
		{strategy: "prefer_native", set: "native_only", wantNativeCall: 1, wantMode: "proxy_pass"},
		{strategy: "prefer_native", set: "chat_only", wantChatCall: 1, wantMode: "responses_server"},
		{strategy: "prefer_native", set: "native_and_chat", wantNativeCall: 1, wantMode: "proxy_pass"},
		// prefer_local_server: local first, native fallback.
		{strategy: "prefer_local_server", set: "native_only", wantNativeCall: 1, wantMode: "proxy_pass"},
		{strategy: "prefer_local_server", set: "chat_only", wantChatCall: 1, wantMode: "responses_server"},
		{strategy: "prefer_local_server", set: "native_and_chat", wantChatCall: 1, wantMode: "responses_server"},
		// native_only: never uses the local server.
		{strategy: "native_only", set: "native_only", wantNativeCall: 1, wantMode: "proxy_pass"},
		{strategy: "native_only", set: "chat_only", wantStatus: http.StatusBadGateway},
		{strategy: "native_only", set: "native_and_chat", wantNativeCall: 1, wantMode: "proxy_pass"},
		// local_server_only: never proxies to a native Responses upstream.
		{strategy: "local_server_only", set: "native_only", wantStatus: http.StatusBadGateway},
		{strategy: "local_server_only", set: "chat_only", wantChatCall: 1, wantMode: "responses_server"},
		{strategy: "local_server_only", set: "native_and_chat", wantChatCall: 1, wantMode: "responses_server"},
	}

	for _, tt := range tests {
		t.Run(tt.strategy+"/"+tt.set, func(t *testing.T) {
			var set upstreamSet
			for _, candidate := range sets {
				if candidate.name == tt.set {
					set = candidate.set
				}
			}

			outputDir := t.TempDir()
			st, err := store.New(outputDir)
			if err != nil {
				t.Fatalf("store.New() error = %v", err)
			}
			defer st.Close()
			if err := st.SaveAppSettingJSON(context.Background(), "routing.settings", map[string]string{"responses_strategy": tt.strategy}); err != nil {
				t.Fatalf("SaveAppSettingJSON() error = %v", err)
			}

			cfg := &config.Config{ResponsesServer: config.ResponsesServerConfig{Enabled: true}}
			var nativeUpstream, chatUpstream *matrixUpstream
			if set.native {
				nativeUpstream = newMatrixUpstream(t, protocolResponses)
				cfg.Upstreams = append(cfg.Upstreams, nativeUpstream.target("native-responses", 200, matrixModel))
			}
			if set.chat {
				chatUpstream = newMatrixUpstream(t, protocolChatCompletions)
				cfg.Upstreams = append(cfg.Upstreams, chatUpstream.target("chat-backend", 100, matrixModel))
			}
			cfg.Debug.OutputDir = outputDir
			cfg.Debug.MaskKey = true

			handler, err := NewHandler(cfg, st)
			if err != nil {
				t.Fatalf("NewHandler() error = %v", err)
			}
			server := httptest.NewServer(handler)
			defer server.Close()

			wantStatus := tt.wantStatus
			if wantStatus == 0 {
				wantStatus = http.StatusOK
			}
			resp := doMatrixRequest(t, server.URL, protocolResponses, matrixModel, false)
			if resp.status != wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", resp.status, wantStatus, resp.body)
			}

			var nativeCalls, chatCalls int32
			if nativeUpstream != nil {
				nativeCalls = nativeUpstream.callCount()
			}
			if chatUpstream != nil {
				chatCalls = chatUpstream.callCount()
			}
			if nativeCalls != tt.wantNativeCall {
				t.Fatalf("native calls = %d, want %d", nativeCalls, tt.wantNativeCall)
			}
			if chatCalls != tt.wantChatCall {
				t.Fatalf("chat calls = %d, want %d", chatCalls, tt.wantChatCall)
			}

			if wantStatus != http.StatusOK {
				return
			}
			recordPath := waitForRecordedHTTPByEndpoint(t, outputDir, "/v1/responses", time.Second)
			parsed, err := waitForRecordedPrelude(recordPath, time.Second)
			if err != nil {
				t.Fatalf("waitForRecordedPrelude(%q) error = %v", recordPath, err)
			}
			plan := routePlanAttrsFromPrelude(t, parsed)
			if plan["execution_mode"] != tt.wantMode {
				t.Fatalf("route plan execution_mode = %v, want %q", plan["execution_mode"], tt.wantMode)
			}
		})
	}
}

// TestHandlerProtocolMatrixStreaming checks that every supported
// entrypoint/upstream pair preserves streaming, including the local Responses
// server synthesizing a Responses SSE stream from a Chat Completions SSE
// upstream.
func TestHandlerProtocolMatrixStreaming(t *testing.T) {
	tests := []struct {
		name        string
		entrypoint  protocolKind
		upstream    protocolKind
		wantMarkers []string
	}{
		{
			name:        "chat_to_chat",
			entrypoint:  protocolChatCompletions,
			upstream:    protocolChatCompletions,
			wantMarkers: []string{"chat.completion.chunk", `"content":"stream "`},
		},
		{
			name:        "responses_to_responses",
			entrypoint:  protocolResponses,
			upstream:    protocolResponses,
			wantMarkers: []string{"event: response.created", "event: response.completed"},
		},
		{
			name:        "responses_to_chat_local_server",
			entrypoint:  protocolResponses,
			upstream:    protocolChatCompletions,
			wantMarkers: []string{"event: response.created", "event: response.output_text.delta", "event: response.completed", `"delta":"pong"`},
		},
		{
			name:        "messages_to_messages",
			entrypoint:  protocolAnthropicMsgs,
			upstream:    protocolAnthropicMsgs,
			wantMarkers: []string{"event: message_start", "event: content_block_delta", "event: message_stop"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kindUpstream := newMatrixUpstream(t, tt.upstream)
			server := newMatrixHandler(t, true, kindUpstream.target("upstream-"+string(tt.upstream), 100, matrixModel))

			resp := doMatrixRequest(t, server.URL, tt.entrypoint, matrixModel, true)
			if resp.status != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", resp.status, resp.body)
			}
			if !strings.HasPrefix(resp.contentType, "text/event-stream") {
				t.Fatalf("Content-Type = %q, want text/event-stream", resp.contentType)
			}
			for _, marker := range tt.wantMarkers {
				if !strings.Contains(resp.body, marker) {
					t.Fatalf("stream body missing marker %q:\n%s", marker, resp.body)
				}
			}

			_, _, upstreamStream, _, _ := kindUpstream.snapshot()
			if !upstreamStream {
				t.Fatalf("upstream stream flag = false, want true for %s", tt.upstream)
			}
		})
	}
}

// TestHandlerResponsesProxyOnlyModeMatrix documents what happens when the local
// Responses server is disabled: /v1/responses is always proxy-passed. A native
// Responses upstream serves it directly; a Chat Completions upstream is still
// addressed at /v1/responses, so such a gateway must implement the Responses
// surface itself or the request fails upstream-side.
func TestHandlerResponsesProxyOnlyModeMatrix(t *testing.T) {
	tests := []struct {
		name             string
		upstream         protocolKind
		wantUpstreamPath string
	}{
		{name: "native_responses_passthrough", upstream: protocolResponses, wantUpstreamPath: "/v1/responses"},
		{name: "chat_upstream_passthrough", upstream: protocolChatCompletions, wantUpstreamPath: "/v1/responses"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kindUpstream := newMatrixUpstream(t, tt.upstream)
			server := newMatrixHandler(t, false, kindUpstream.target("upstream-"+string(tt.upstream), 100, matrixModel))

			resp := doMatrixRequest(t, server.URL, protocolResponses, matrixModel, false)
			if resp.status != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", resp.status, resp.body)
			}
			if got := kindUpstream.callCount(); got != 1 {
				t.Fatalf("upstream calls = %d, want 1", got)
			}
			path, _, _, _, _ := kindUpstream.snapshot()
			if path != tt.wantUpstreamPath {
				t.Fatalf("upstream path = %q, want %q", path, tt.wantUpstreamPath)
			}
		})
	}
}
