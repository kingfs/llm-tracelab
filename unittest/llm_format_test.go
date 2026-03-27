package unittest

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/pkg/llm"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"

	"github.com/stretchr/testify/assert"
)

// 读取 .http 文件并解析为 Raw HTTP Request + Response
func loadRecordedHTTP(path string) (req string, resp string, err error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		return "", "", err
	}
	_, reqBodyBytes, _, resBodyBytes := recordfile.ExtractSections(content, parsed)
	req = string(reqBodyBytes)
	resp = string(resBodyBytes)
	return req, resp, nil
}

// 根据 URL 判断厂商
func detectVendor(req *http.Request) string {
	u := req.URL.String()
	switch {
	case strings.Contains(u, "api.openai.com"):
		return "openai"
	case strings.Contains(u, "api.anthropic.com"):
		return "anthropic"
	case strings.Contains(u, "generativelanguage.googleapis.com"):
		return "gemini"
	default:
		return "unknown"
	}
}

func TestReplayLLMRequestResponse(t *testing.T) {
	req, resp, err := loadRecordedHTTP("testdata/non-stream.http")
	assert.NoError(t, err)

	reqBody := []byte(req)
	respBody := []byte(resp)

	// vendor := detectVendor(req)
	// assert.NotEqual(t, "unknown", vendor)

	var llmReq llm.LLMRequest
	var llmResp llm.LLMResponse
	var oreq llm.OpenAIChatRequest
	json.Unmarshal(reqBody, &oreq)
	llmReq = llm.FromOpenAIRequest(oreq)

	var ores llm.OpenAIChatResponse
	json.Unmarshal(respBody, &ores)
	llmResp = llm.OpenAIToLLM(ores)

	// switch vendor {
	// case "openai":
	// 	var oreq llm.OpenAIChatRequest
	// 	json.Unmarshal(reqBody, &oreq)
	// 	llmReq = llm.FromOpenAIRequest(oreq)

	// 	var ores llm.OpenAIChatResponse
	// 	json.Unmarshal(respBody, &ores)
	// 	llmResp = llm.OpenAIToLLM(ores)

	// case "anthropic":
	// 	var areq llm.AnthropicRequest
	// 	json.Unmarshal(reqBody, &areq)
	// 	llmReq = llm.FromAnthropicRequest(areq)

	// 	var ares llm.AnthropicResponse
	// 	json.Unmarshal(respBody, &ares)
	// 	llmResp = llm.AnthropicToLLM(ares)

	// case "gemini":
	// 	var greq llm.GeminiGenerateContentRequest
	// 	json.Unmarshal(reqBody, &greq)
	// 	llmReq = llm.FromGeminiRequest(greq)

	// 	var gres llm.GeminiResponse
	// 	json.Unmarshal(respBody, &gres)
	// 	llmResp = llm.GeminiToLLM(gres)
	// }

	// ---- Assertions ----
	assert.NotEmpty(t, llmReq.Model)
	if len(llmReq.System) < 1 && len(llmReq.Messages) < 1 {
		t.Error("no message")
	}
	assert.NotEmpty(t, llmResp.Candidates)
	assert.NotEmpty(t, llmResp.Candidates[0].Content)
}
