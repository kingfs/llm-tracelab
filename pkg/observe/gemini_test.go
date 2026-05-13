package observe

import (
	"strings"
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

func TestGeminiParserPreservesMultimodalPartMetadata(t *testing.T) {
	parser := NewGeminiParser()
	obs, err := parser.Parse(t.Context(), ParseInput{
		TraceID: "trace-gemini-multimodal",
		Header:  geminiTestHeader("google_genai", "/v1beta/models:generateContent", false),
		RequestBody: []byte(`{
			"model":"models/gemini-2.5-flash",
			"contents":[{
				"role":"user",
				"parts":[
					{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U="},"partMetadata":{"source":"clipboard"}},
					{"fileData":{"mimeType":"application/pdf","fileUri":"gs://bucket/doc.pdf"},"videoMetadata":{"startOffset":"1s","endOffset":"2s"}}
				]
			}]
		}`),
		ResponseBody: []byte(`{
			"candidates":[{
				"content":{"role":"model","parts":[
					{"text":"I inspected the files.","thought":true,"thoughtSignature":"sig_multimodal"},
					{"text":"Done"}
				]}
			}]
		}`),
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(obs.Request.Messages) != 1 || len(obs.Request.Messages[0].Children) != 2 {
		t.Fatalf("request messages = %+v", obs.Request.Messages)
	}
	imageNode := obs.Request.Messages[0].Children[0]
	if imageNode.ProviderType != "inlineData" || imageNode.NormalizedType != NodeImage {
		t.Fatalf("image node = %+v", imageNode)
	}
	if _, ok := imageNode.Metadata["partMetadata"]; !ok {
		t.Fatalf("partMetadata missing from image node metadata: %+v", imageNode.Metadata)
	}
	fileNode := obs.Request.Messages[0].Children[1]
	if fileNode.ProviderType != "fileData" || fileNode.NormalizedType != NodeFile {
		t.Fatalf("file node = %+v", fileNode)
	}
	if !strings.Contains(string(fileNode.Raw), `"videoMetadata"`) {
		t.Fatalf("videoMetadata raw not preserved: %s", fileNode.Raw)
	}
	if len(obs.Response.Reasoning) != 1 || obs.Response.Reasoning[0].Text != "I inspected the files." {
		t.Fatalf("reasoning = %+v", obs.Response.Reasoning)
	}
	if obs.Response.Reasoning[0].Metadata["thoughtSignature"] != "sig_multimodal" {
		t.Fatalf("thought signature metadata = %+v", obs.Response.Reasoning[0].Metadata)
	}
}

func TestGeminiParserParsesNonStreamProviderError(t *testing.T) {
	parser := NewGeminiParser()
	obs, err := parser.Parse(t.Context(), ParseInput{
		TraceID:      "trace-gemini-error",
		Header:       geminiTestHeader("google_genai", "/v1beta/models:generateContent", false),
		RequestBody:  []byte(`{"model":"models/gemini-2.5-flash","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`),
		ResponseBody: []byte(`{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`),
	})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(obs.Response.Errors) != 1 || obs.Response.Errors[0].NormalizedType != NodeError {
		t.Fatalf("errors = %+v", obs.Response.Errors)
	}
	if !strings.Contains(obs.Response.Errors[0].Text, "Quota exceeded") {
		t.Fatalf("error text = %q", obs.Response.Errors[0].Text)
	}
	if len(obs.Response.Candidates) != 0 {
		t.Fatalf("candidates = %+v", obs.Response.Candidates)
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
