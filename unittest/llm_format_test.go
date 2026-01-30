package unittest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/recorder"
	"github.com/kingfs/llm-tracelab/pkg/llm"

	"github.com/stretchr/testify/assert"
)

// 读取 .http 文件并解析为 Raw HTTP Request + Response
func loadRecordedHTTP(path string) (req string, resp string, err error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	reader := bytes.NewReader(content)
	bufReader := bufio.NewReader(reader)

	// 1. 读取第一行 Header JSON
	line1, err := bufReader.ReadString('\n')
	if err != nil {
		return "", "", fmt.Errorf("failed to read header line: %w", err)
	}

	var header recorder.RecordHeader
	if err := json.Unmarshal([]byte(line1), &header); err != nil {
		return "", "", fmt.Errorf("invalid header json: %w", err)
	}

	// 2. 提取 Full Request (Header + Body)
	// Base Offset = 2048
	baseOffset := int64(recorder.HeaderLen)

	reqEndOffset := baseOffset + header.Layout.ReqHeaderLen + header.Layout.ReqBodyLen
	if reqEndOffset > int64(len(content)) {
		reqEndOffset = int64(len(content))
	}

	// reqFullBytes := content[baseOffset:reqEndOffset]

	// 提取 Request Body 用于解析 Messages
	// Body Start = Base + ReqHeaderLen
	reqBodyStart := baseOffset + header.Layout.ReqHeaderLen
	reqBodyBytes := content[reqBodyStart:reqEndOffset]
	req = string(reqBodyBytes)

	// 3. 提取 Full Response (Header + Body)
	// Base = ReqEnd + 1 (\n)
	resStartOffset := reqEndOffset + 1

	// var resFullBytes []byte
	var resBodyBytes []byte

	if resStartOffset < int64(len(content)) {
		// Response Header + Body
		// resFullBytes = content[resStartOffset:]

		// Response Body Only (for AI content parsing)
		// Body Start = ResStart + ResHeaderLen
		resBodyStart := resStartOffset + header.Layout.ResHeaderLen
		if resBodyStart < int64(len(content)) {
			resBodyBytes = content[resBodyStart:]
		}
	}
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

	var llmResp llm.LLMResponse
	var oreq llm.OpenAIChatRequest
	json.Unmarshal(reqBody, &oreq)

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
	assert.NotEmpty(t, oreq.Model)
	assert.NotEmpty(t, oreq.Messages)
	assert.NotEmpty(t, llmResp.Candidates)
	assert.NotEmpty(t, llmResp.Candidates[0].Content)
}
