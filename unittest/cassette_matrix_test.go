package unittest

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/pkg/llm"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	"github.com/kingfs/llm-tracelab/pkg/replay"
)

type cassetteSpec struct {
	provider        string
	operation       string
	endpoint        string
	url             string
	method          string
	model           string
	requestProtocol string
	requestBody     string
	responseStatus  string
	responseHeaders string
	responseBody    string
	isStream        bool
	usage           recordfile.UsageInfo
}

func TestCassetteMatrixReplayAndParse(t *testing.T) {
	tests := []struct {
		name                 string
		spec                 cassetteSpec
		wantReplayContains   string
		wantMessageContains  string
		wantAIContent        string
		wantPromptTokens     int
		wantCompletionTokens int
	}{
		{
			name: "openai_responses_non_stream",
			spec: cassetteSpec{
				provider:        llm.ProviderOpenAICompatible,
				operation:       llm.OperationResponses,
				endpoint:        "/v1/responses",
				url:             "/v1/responses",
				method:          http.MethodPost,
				model:           "gpt-5",
				requestProtocol: "POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
				requestBody:     `{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello from openai"}]}]}`,
				responseStatus:  "200 OK",
				responseHeaders: "Content-Type: application/json\r\n",
				responseBody:    `{"id":"resp_1","model":"gpt-5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello from assistant"}]}],"usage":{"input_tokens":3,"output_tokens":5,"total_tokens":8}}`,
				usage: recordfile.UsageInfo{
					PromptTokens:     3,
					CompletionTokens: 5,
					TotalTokens:      8,
				},
			},
			wantReplayContains:   `"total_tokens":8`,
			wantMessageContains:  "hello from openai",
			wantAIContent:        "hello from assistant",
			wantPromptTokens:     3,
			wantCompletionTokens: 5,
		},
		{
			name: "anthropic_messages_non_stream",
			spec: cassetteSpec{
				provider:        llm.ProviderAnthropic,
				operation:       llm.OperationMessages,
				endpoint:        "/v1/messages",
				url:             "/v1/messages",
				method:          http.MethodPost,
				model:           "claude-sonnet-4-5",
				requestProtocol: "POST /v1/messages HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
				requestBody:     `{"model":"claude-sonnet-4-5","system":"Be concise","messages":[{"role":"user","content":[{"type":"text","text":"hello from anthropic"}]}],"max_tokens":16}`,
				responseStatus:  "200 OK",
				responseHeaders: "Content-Type: application/json\r\n",
				responseBody:    `{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"hello from claude"}],"usage":{"input_tokens":4,"output_tokens":6}}`,
				usage: recordfile.UsageInfo{
					PromptTokens:     4,
					CompletionTokens: 6,
					TotalTokens:      10,
				},
			},
			wantReplayContains:   `"output_tokens":6`,
			wantMessageContains:  "hello from anthropic",
			wantAIContent:        "hello from claude",
			wantPromptTokens:     4,
			wantCompletionTokens: 6,
		},
		{
			name: "google_genai_stream",
			spec: cassetteSpec{
				provider:        llm.ProviderGoogleGenAI,
				operation:       llm.OperationGenerateContent,
				endpoint:        "/v1beta/models:streamGenerateContent",
				url:             "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
				method:          http.MethodPost,
				model:           "gemini-2.5-flash",
				requestProtocol: "POST /v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n",
				requestBody:     `{"contents":[{"role":"user","parts":[{"text":"hello from gemini"}]}]}`,
				responseStatus:  "200 OK",
				responseHeaders: "Content-Type: text/event-stream\r\n",
				responseBody: "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hello \"}]}}]}\n\n" +
					"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Gemini\"}]}}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":7,\"totalTokenCount\":10}}\n\n",
				isStream: true,
				usage: recordfile.UsageInfo{
					PromptTokens:     3,
					CompletionTokens: 7,
					TotalTokens:      10,
				},
			},
			wantReplayContains:   "data:",
			wantMessageContains:  "hello from gemini",
			wantAIContent:        "Hello Gemini",
			wantPromptTokens:     3,
			wantCompletionTokens: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, content := writeCassetteFixture(t, tt.spec)

			parsedPrelude, err := recordfile.ParsePrelude(content)
			if err != nil {
				t.Fatalf("ParsePrelude() error = %v", err)
			}
			if parsedPrelude.Header.Meta.Provider != tt.spec.provider {
				t.Fatalf("provider = %q, want %q", parsedPrelude.Header.Meta.Provider, tt.spec.provider)
			}
			if parsedPrelude.Header.Meta.Operation != tt.spec.operation {
				t.Fatalf("operation = %q, want %q", parsedPrelude.Header.Meta.Operation, tt.spec.operation)
			}

			reqFull, reqBody, _, resBody := recordfile.ExtractSections(content, parsedPrelude)
			if len(reqFull) == 0 || len(reqBody) == 0 || len(resBody) == 0 {
				t.Fatalf("extracted sections should not be empty")
			}

			client := &http.Client{Transport: replay.NewTransport(path)}
			req, err := http.NewRequest(tt.spec.method, "http://localhost"+tt.spec.url, bytes.NewBufferString(tt.spec.requestBody))
			if err != nil {
				t.Fatalf("http.NewRequest() error = %v", err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("client.Do() error = %v", err)
			}
			replayedBody, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatalf("io.ReadAll() error = %v", err)
			}
			if !bytes.Contains(replayedBody, []byte(tt.wantReplayContains)) {
				t.Fatalf("replayed body = %q, want contain %q", string(replayedBody), tt.wantReplayContains)
			}

			llmReq, err := llm.ParseRequestForPath(tt.spec.url, "https://upstream.example", reqBody)
			if err != nil {
				t.Fatalf("ParseRequestForPath() error = %v", err)
			}
			if llmReq.Model != tt.spec.model {
				t.Fatalf("request model = %q, want %q", llmReq.Model, tt.spec.model)
			}

			var llmResp llm.LLMResponse
			if tt.spec.isStream {
				llmResp, err = llm.ParseStreamResponseForPath(tt.spec.url, "https://upstream.example", resBody)
			} else {
				llmResp, err = llm.ParseResponseForPath(tt.spec.url, "https://upstream.example", resBody)
			}
			if err != nil {
				t.Fatalf("parse response error = %v", err)
			}
			if len(llmResp.Candidates) == 0 || len(llmResp.Candidates[0].Content) == 0 {
				t.Fatalf("llm response should contain candidate content")
			}
			if llmResp.Candidates[0].Content[0].Text != tt.wantAIContent {
				t.Fatalf("content = %q, want %q", llmResp.Candidates[0].Content[0].Text, tt.wantAIContent)
			}

			parsedMonitor, err := monitor.ParseLogFile(content)
			if err != nil {
				t.Fatalf("monitor.ParseLogFile() error = %v", err)
			}
			if len(parsedMonitor.ChatMessages) == 0 {
				t.Fatalf("monitor chat messages should not be empty")
			}
			if parsedMonitor.Header.Usage.PromptTokens != tt.wantPromptTokens {
				t.Fatalf("prompt tokens = %d, want %d", parsedMonitor.Header.Usage.PromptTokens, tt.wantPromptTokens)
			}
			if parsedMonitor.Header.Usage.CompletionTokens != tt.wantCompletionTokens {
				t.Fatalf("completion tokens = %d, want %d", parsedMonitor.Header.Usage.CompletionTokens, tt.wantCompletionTokens)
			}
			if parsedMonitor.AIContent != tt.wantAIContent {
				t.Fatalf("monitor AIContent = %q, want %q", parsedMonitor.AIContent, tt.wantAIContent)
			}
			foundMessage := false
			for _, message := range parsedMonitor.ChatMessages {
				if bytes.Contains([]byte(message.Content), []byte(tt.wantMessageContains)) {
					foundMessage = true
					break
				}
			}
			if !foundMessage {
				t.Fatalf("chat messages do not contain %q: %+v", tt.wantMessageContains, parsedMonitor.ChatMessages)
			}
		})
	}
}

func writeCassetteFixture(t *testing.T, spec cassetteSpec) (string, []byte) {
	t.Helper()

	reqHead := []byte(spec.requestProtocol)
	reqBody := []byte(spec.requestBody)
	resHead := []byte("HTTP/1.1 " + spec.responseStatus + "\r\n" + spec.responseHeaders + "\r\n")
	resBody := []byte(spec.responseBody)

	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: recordfile.MetaData{
			RequestID:  "test-" + spec.provider,
			Time:       time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC),
			Model:      spec.model,
			Provider:   spec.provider,
			Operation:  spec.operation,
			Endpoint:   spec.endpoint,
			URL:        spec.url,
			Method:     spec.method,
			StatusCode: 200,
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: int64(len(reqHead)),
			ReqBodyLen:   int64(len(reqBody)),
			ResHeaderLen: int64(len(resHead)),
			ResBodyLen:   int64(len(resBody)),
			IsStream:     spec.isStream,
		},
		Usage: spec.usage,
	}

	prelude, err := recordfile.MarshalPrelude(header, recordfile.BuildEvents(header))
	if err != nil {
		t.Fatalf("MarshalPrelude() error = %v", err)
	}

	content := append([]byte{}, prelude...)
	content = append(content, reqHead...)
	content = append(content, reqBody...)
	content = append(content, '\n')
	content = append(content, resHead...)
	content = append(content, resBody...)

	path := filepath.Join(t.TempDir(), spec.provider+".http")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path, content
}
