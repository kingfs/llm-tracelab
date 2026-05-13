package observe

import (
	"testing"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func TestGeminiParserParsesGenerateContent(t *testing.T) {
	parser := NewGeminiParser()
	obs, err := parser.Parse(t.Context(), ParseInput{
		TraceID: "trace-gemini",
		Header:  geminiTestHeader("google_genai", "/v1beta/models:generateContent", false),
		RequestBody: []byte(`{
			"model":"models/gemini-2.5-flash",
			"systemInstruction":{"role":"system","parts":[{"text":"Be concise."}]},
			"contents":[
				{"role":"user","parts":[{"text":"hello"},{"functionResponse":{"name":"lookup","response":{"value":1}}}]}
			],
			"tools":[{"functionDeclarations":[{"name":"lookup","description":"Lookup value","parameters":{"type":"object"}}]},{"codeExecution":{}}],
			"generationConfig":{"maxOutputTokens":64}
		}`),
		ResponseBody: []byte(`{
			"candidates":[{
				"index":0,
				"content":{"role":"model","parts":[
					{"text":"Hi"},
					{"text":"internal plan","thought":true,"thoughtSignature":"abc"},
					{"functionCall":{"name":"lookup","args":{"q":"x"}}},
					{"executableCode":{"language":"PYTHON","code":"print(1)"}},
					{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":"1"}},
					{"toolCall":{"name":"googleSearch","args":{"query":"docs"}}},
					{"toolResponse":{"name":"googleSearch","response":{"ok":true}}}
				]},
				"finishReason":"SAFETY",
				"safetyRatings":[{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","probability":"HIGH","blocked":true}]
			}],
			"promptFeedback":{"blockReason":"SAFETY"},
			"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":7,"totalTokenCount":10}
		}`),
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if obs.Parser != "gemini" || obs.Model != "models/gemini-2.5-flash" {
		t.Fatalf("parser/model = %s/%s", obs.Parser, obs.Model)
	}
	if len(obs.Request.Instructions) != 1 || obs.Request.Instructions[0].Text != "Be concise." {
		t.Fatalf("instructions = %+v", obs.Request.Instructions)
	}
	if len(obs.Tools.Declarations) != 2 {
		t.Fatalf("tool declarations = %+v", obs.Tools.Declarations)
	}
	if len(obs.Response.Reasoning) != 1 || obs.Response.Reasoning[0].Text != "internal plan" {
		t.Fatalf("reasoning = %+v", obs.Response.Reasoning)
	}
	if len(obs.Tools.Calls) < 2 {
		t.Fatalf("tool calls = %+v", obs.Tools.Calls)
	}
	var foundServer bool
	for _, call := range obs.Tools.Calls {
		if call.Kind == "toolCall" && call.Owner == ToolOwnerProviderExecuted {
			foundServer = true
		}
	}
	if !foundServer {
		t.Fatalf("server tool call missing in %+v", obs.Tools.Calls)
	}
	if len(obs.Tools.Results) < 2 {
		t.Fatalf("tool results = %+v", obs.Tools.Results)
	}
	if !obs.Safety.Blocked {
		t.Fatalf("Safety.Blocked = false")
	}
	if obs.Usage.TotalTokens != 10 {
		t.Fatalf("usage = %+v", obs.Usage)
	}
}

func TestGeminiParserParsesStreamGenerateContent(t *testing.T) {
	parser := NewGeminiParser()
	body := joinSSE(
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello "}]}}]}`,
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Gemini"},{"functionCall":{"name":"lookup","args":{"q":"x"}}}]}}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":7,"totalTokenCount":10}}`,
	)
	obs, err := parser.Parse(t.Context(), ParseInput{
		TraceID:      "trace-gemini-stream",
		Header:       geminiTestHeader("vertex_native", "/v1/publishers/models:streamGenerateContent", true),
		RequestBody:  []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`),
		ResponseBody: []byte(body),
		IsStream:     true,
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if obs.Stream.AccumulatedText != "Hello Gemini" {
		t.Fatalf("stream text = %q", obs.Stream.AccumulatedText)
	}
	if len(obs.Tools.Calls) != 1 || obs.Tools.Calls[0].Name != "lookup" {
		t.Fatalf("tool calls = %+v", obs.Tools.Calls)
	}
	if obs.Usage.TotalTokens != 10 {
		t.Fatalf("usage = %+v", obs.Usage)
	}
}

func geminiTestHeader(provider string, endpoint string, isStream bool) recordfile.RecordHeader {
	return recordfile.RecordHeader{
		Meta: recordfile.MetaData{
			Provider:  provider,
			Operation: "generate_content",
			Endpoint:  endpoint,
		},
		Layout: recordfile.LayoutInfo{IsStream: isStream},
	}
}
