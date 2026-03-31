package unittest

import (
	"os"
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

func TestReplayLLMRequestResponse(t *testing.T) {
	req, resp, err := loadRecordedHTTP("testdata/non-stream.http")
	assert.NoError(t, err)

	reqBody := []byte(req)
	respBody := []byte(resp)

	llmReq, err := llm.ParseRequest(llm.ProviderOpenAICompatible, "/v1/chat/completions", reqBody)
	assert.NoError(t, err)
	llmResp, err := llm.ParseResponse(llm.ProviderOpenAICompatible, "/v1/chat/completions", respBody)
	assert.NoError(t, err)

	// ---- Assertions ----
	assert.NotEmpty(t, llmReq.Model)
	if len(llmReq.System) < 1 && len(llmReq.Messages) < 1 {
		t.Error("no message")
	}
	assert.NotEmpty(t, llmResp.Candidates)
	assert.NotEmpty(t, llmResp.Candidates[0].Content)
}
