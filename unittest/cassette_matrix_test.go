package unittest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/monitor"
	"github.com/kingfs/llm-tracelab/pkg/llm"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	"github.com/kingfs/llm-tracelab/pkg/replay"
)

func TestCassetteMatrixReplayAndParse(t *testing.T) {
	for _, tt := range cassetteFixtureCatalog() {
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
			if len(tt.want.eventTypes) > 0 {
				foundTypes := map[string]bool{}
				for _, event := range parsedPrelude.Events {
					foundTypes[event.Type] = true
				}
				for _, eventType := range tt.want.eventTypes {
					if !foundTypes[eventType] {
						t.Fatalf("missing event type %q in %+v", eventType, parsedPrelude.Events)
					}
				}
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
			if !bytes.Contains(replayedBody, []byte(tt.want.replayContains)) {
				t.Fatalf("replayed body = %q, want contain %q", string(replayedBody), tt.want.replayContains)
			}
			if tt.want.statusCode > 0 && resp.StatusCode != tt.want.statusCode {
				t.Fatalf("replayed status code = %d, want %d", resp.StatusCode, tt.want.statusCode)
			}

			llmReq, err := llm.ParseRequestForPath(tt.spec.url, "https://upstream.example", reqBody)
			if err != nil {
				t.Fatalf("ParseRequestForPath() error = %v", err)
			}
			if llmReq.Model != tt.spec.model {
				t.Fatalf("request model = %q, want %q", llmReq.Model, tt.spec.model)
			}
			if tt.want.messageCount > 0 && requestHistoryCount(llmReq) != tt.want.messageCount {
				t.Fatalf("request history count = %d, want %d", requestHistoryCount(llmReq), tt.want.messageCount)
			}
			if tt.want.toolResultText != "" {
				foundToolResult := false
				for _, msg := range llmReq.Messages {
					for _, content := range msg.Content {
						if content.Type != "tool_result" {
							continue
						}
						if bytes.Contains([]byte(stringifyMap(content.ToolResult)), []byte(tt.want.toolResultText)) {
							foundToolResult = true
							break
						}
					}
					if foundToolResult {
						break
					}
				}
				if !foundToolResult {
					t.Fatalf("request tool_result not found for %q: %+v", tt.want.toolResultText, llmReq.Messages)
				}
			}
			for _, expected := range tt.want.historyContains {
				foundHistory := false
				for _, content := range llmReq.System {
					if bytes.Contains([]byte(content.Text), []byte(expected)) || bytes.Contains([]byte(content.Refusal), []byte(expected)) || bytes.Contains([]byte(stringifyMap(content.ToolResult)), []byte(expected)) {
						foundHistory = true
						break
					}
				}
				for _, msg := range llmReq.Messages {
					for _, content := range msg.Content {
						if bytes.Contains([]byte(content.Text), []byte(expected)) || bytes.Contains([]byte(content.Refusal), []byte(expected)) || bytes.Contains([]byte(stringifyMap(content.ToolResult)), []byte(expected)) {
							foundHistory = true
							break
						}
					}
					if foundHistory {
						break
					}
				}
				if !foundHistory {
					t.Fatalf("request history does not contain %q: %+v", expected, llmReq.Messages)
				}
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
			if len(llmResp.Candidates) == 0 {
				t.Fatalf("llm response should contain at least one candidate")
			}
			gotContent := ""
			for _, content := range llmResp.Candidates[0].Content {
				if content.Type == "text" {
					gotContent = content.Text
					break
				}
			}
			if gotContent != tt.want.aiContent {
				t.Fatalf("content = %q, want %q", gotContent, tt.want.aiContent)
			}
			if tt.want.blockContains != "" {
				matchedBlock := llmResp.Candidates[0].Refusal != nil && bytes.Contains([]byte(llmResp.Candidates[0].Refusal.Message), []byte(tt.want.blockContains))
				if !matchedBlock {
					payload, _ := json.Marshal(llmResp.Extensions["error"])
					matchedBlock = bytes.Contains(payload, []byte(tt.want.blockContains))
				}
				if !matchedBlock {
					payload, _ := json.Marshal(llmResp.Extensions["model_list"])
					matchedBlock = bytes.Contains(payload, []byte(tt.want.blockContains))
				}
				if !matchedBlock {
					t.Fatalf("llm response block does not contain %q: refusal=%+v extensions=%+v", tt.want.blockContains, llmResp.Candidates[0].Refusal, llmResp.Extensions)
				}
			}
			if tt.want.toolCallName != "" {
				if len(llmResp.Candidates[0].ToolCalls) == 0 {
					t.Fatalf("expected tool call %q, got none", tt.want.toolCallName)
				}
				if llmResp.Candidates[0].ToolCalls[0].Name != tt.want.toolCallName {
					t.Fatalf("tool call = %q, want %q", llmResp.Candidates[0].ToolCalls[0].Name, tt.want.toolCallName)
				}
			}

			parsedMonitor, err := monitor.ParseLogFile(content)
			if err != nil {
				t.Fatalf("monitor.ParseLogFile() error = %v", err)
			}
			if len(parsedMonitor.ChatMessages) == 0 {
				t.Fatalf("monitor chat messages should not be empty")
			}
			if tt.want.messageCount > 0 && len(parsedMonitor.ChatMessages) != tt.want.messageCount {
				t.Fatalf("monitor chat messages = %d, want %d", len(parsedMonitor.ChatMessages), tt.want.messageCount)
			}
			if parsedMonitor.Header.Usage.PromptTokens != tt.want.promptTokens {
				t.Fatalf("prompt tokens = %d, want %d", parsedMonitor.Header.Usage.PromptTokens, tt.want.promptTokens)
			}
			if parsedMonitor.Header.Usage.CompletionTokens != tt.want.completionTokens {
				t.Fatalf("completion tokens = %d, want %d", parsedMonitor.Header.Usage.CompletionTokens, tt.want.completionTokens)
			}
			if tt.want.statusCode > 0 && parsedMonitor.Header.Meta.StatusCode != tt.want.statusCode {
				t.Fatalf("monitor status code = %d, want %d", parsedMonitor.Header.Meta.StatusCode, tt.want.statusCode)
			}
			if parsedMonitor.AIContent != tt.want.aiContent {
				t.Fatalf("monitor AIContent = %q, want %q", parsedMonitor.AIContent, tt.want.aiContent)
			}
			if tt.want.aiBlockCount > 0 && len(parsedMonitor.AIBlocks) != tt.want.aiBlockCount {
				t.Fatalf("monitor AIBlocks = %d, want %d", len(parsedMonitor.AIBlocks), tt.want.aiBlockCount)
			}
			if parsedMonitor.AIReasoning != tt.want.aiReasoning {
				t.Fatalf("monitor AIReasoning = %q, want %q", parsedMonitor.AIReasoning, tt.want.aiReasoning)
			}
			if tt.want.toolCallName != "" {
				if len(parsedMonitor.ResponseToolCalls) == 0 {
					t.Fatalf("expected monitor tool call %q, got none", tt.want.toolCallName)
				}
				if parsedMonitor.ResponseToolCalls[0].Function.Name != tt.want.toolCallName {
					t.Fatalf("monitor tool call = %q, want %q", parsedMonitor.ResponseToolCalls[0].Function.Name, tt.want.toolCallName)
				}
			}
			foundMessage := false
			for _, message := range parsedMonitor.ChatMessages {
				if bytes.Contains([]byte(message.Content), []byte(tt.want.messageContains)) {
					foundMessage = true
					break
				}
			}
			if !foundMessage {
				t.Fatalf("chat messages do not contain %q: %+v", tt.want.messageContains, parsedMonitor.ChatMessages)
			}
			for _, expected := range tt.want.historyContains {
				foundHistory := false
				for _, message := range parsedMonitor.ChatMessages {
					if bytes.Contains([]byte(message.Content), []byte(expected)) {
						foundHistory = true
						break
					}
				}
				if !foundHistory {
					t.Fatalf("chat history does not contain %q: %+v", expected, parsedMonitor.ChatMessages)
				}
			}
			if tt.want.blockContains != "" {
				foundBlock := false
				for _, block := range parsedMonitor.AIBlocks {
					if bytes.Contains([]byte(block.Text), []byte(tt.want.blockContains)) {
						foundBlock = true
						break
					}
				}
				if !foundBlock {
					t.Fatalf("ai blocks do not contain %q: %+v", tt.want.blockContains, parsedMonitor.AIBlocks)
				}
			}
			for _, title := range tt.want.aiBlockTitles {
				foundTitle := false
				for _, block := range parsedMonitor.AIBlocks {
					if block.Title == title {
						foundTitle = true
						break
					}
				}
				if !foundTitle {
					t.Fatalf("ai block title %q not found: %+v", title, parsedMonitor.AIBlocks)
				}
			}
			if tt.want.errorContent != "" {
				foundError := false
				for _, message := range parsedMonitor.ChatMessages {
					if message.IsError && bytes.Contains([]byte(message.Content), []byte(tt.want.errorContent)) {
						foundError = true
						break
					}
				}
				if !foundError {
					t.Fatalf("error chat message not found for %q: %+v", tt.want.errorContent, parsedMonitor.ChatMessages)
				}
			}
			if tt.want.toolResultType != "" {
				foundToolResult := false
				for _, message := range parsedMonitor.ChatMessages {
					if message.MessageType == tt.want.toolResultType && bytes.Contains([]byte(message.Content), []byte(tt.want.toolResultText)) {
						foundToolResult = true
						break
					}
				}
				if !foundToolResult {
					t.Fatalf("tool result message not found for %q/%q: %+v", tt.want.toolResultType, tt.want.toolResultText, parsedMonitor.ChatMessages)
				}
			}
		})
	}
}

func TestCassetteFixtureCatalogCoverage(t *testing.T) {
	fixtures := cassetteFixtureCatalog()
	if len(fixtures) == 0 {
		t.Fatal("fixture catalog should not be empty")
	}

	requiredCapabilities := []cassetteCapability{
		capabilityNonStream,
		capabilityStream,
		capabilityReasoning,
		capabilityToolCall,
		capabilityToolResult,
		capabilityMultiTurn,
		capabilityHistory,
		capabilityMixedBlocks,
		capabilitySafety,
		capabilityProviderErr,
		capabilityStreamError,
		capabilityPartialComp,
		capabilityRefusal,
		capabilityError,
		capabilityModelList,
	}

	for _, capability := range requiredCapabilities {
		count := 0
		providers := map[string]bool{}
		for _, fixture := range fixtures {
			if !fixture.hasCapability(capability) {
				continue
			}
			count++
			providers[fixture.spec.provider] = true
		}
		if count == 0 {
			t.Fatalf("missing fixture coverage for capability %q", capability)
		}
		if capability == capabilityStream && len(providers) < 3 {
			t.Fatalf("stream coverage should span 3 providers, got %d: %+v", len(providers), providers)
		}
		if capability == capabilityProviderErr && len(providers) < 3 {
			t.Fatalf("provider error coverage should span 3 providers, got %d: %+v", len(providers), providers)
		}
		if capability == capabilityStreamError && len(providers) < 3 {
			t.Fatalf("stream error coverage should span 3 providers, got %d: %+v", len(providers), providers)
		}
		if capability == capabilityPartialComp {
			if len(providers) < 3 {
				t.Fatalf("partial completion coverage should span 3 providers, got %d: %+v", len(providers), providers)
			}
			for _, fixture := range fixtures {
				if !fixture.hasCapability(capabilityPartialComp) {
					continue
				}
				if fixture.want.aiContent == "" {
					t.Fatalf("partial completion fixture %q should keep partial ai content", fixture.name)
				}
				if fixture.want.blockContains == "" {
					t.Fatalf("partial completion fixture %q should assert terminating block", fixture.name)
				}
				if fixture.want.aiBlockCount == 0 {
					t.Fatalf("partial completion fixture %q should expect ai blocks", fixture.name)
				}
			}
		}
		if capability == capabilitySafety {
			if !providers[llm.ProviderGoogleGenAI] {
				t.Fatalf("safety coverage should include %q fixtures, got %+v", llm.ProviderGoogleGenAI, providers)
			}

			foundPromptFeedback := false
			foundSafetyRatings := false
			for _, fixture := range fixtures {
				if !fixture.hasCapability(capabilitySafety) {
					continue
				}
				for _, title := range fixture.want.aiBlockTitles {
					if title == "Prompt Feedback" {
						foundPromptFeedback = true
					}
					if title == "Safety Ratings" {
						foundSafetyRatings = true
					}
				}
				if fixture.want.blockContains != "" {
					foundPromptFeedback = true
				}
			}
			if !foundPromptFeedback {
				t.Fatalf("safety coverage should assert prompt feedback or refusal blocks")
			}
			if !foundSafetyRatings {
				t.Fatalf("safety coverage should assert safety rating blocks")
			}
		}
	}
}

func stringifyMap(v map[string]any) string {
	if len(v) == 0 {
		return ""
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

func requestHistoryCount(req llm.LLMRequest) int {
	count := len(req.Messages)
	if len(req.System) > 0 {
		count++
	}
	return count
}
