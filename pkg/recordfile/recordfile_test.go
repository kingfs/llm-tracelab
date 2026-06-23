package recordfile

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalAndParsePreludeV3(t *testing.T) {
	header := RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: MetaData{
			RequestID:  "req-1",
			Time:       time.Date(2026, 3, 26, 10, 0, 0, 0, time.UTC),
			Model:      "gpt-4.1",
			URL:        "/v1/chat/completions",
			Method:     "POST",
			StatusCode: 200,
		},
		Layout: LayoutInfo{
			ReqHeaderLen: 100,
			ReqBodyLen:   50,
			ResHeaderLen: 80,
			ResBodyLen:   120,
			IsStream:     true,
		},
		Usage: UsageInfo{
			PromptTokens:     11,
			CompletionTokens: 7,
			TotalTokens:      18,
		},
	}

	prelude, err := MarshalPrelude(header, BuildEvents(header))
	require.NoError(t, err)

	content := append(prelude, []byte("REQUEST\nRESPONSE")...)
	parsed, err := ParsePrelude(content)
	require.NoError(t, err)

	assert.Equal(t, header, parsed.Header)
	assert.Len(t, parsed.Events, 2)
	assert.Equal(t, int64(len(prelude)), parsed.PayloadOffset)
}

func TestMarshalAndParsePreludeV3PreservesEventAttributes(t *testing.T) {
	header := RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: MetaData{
			RequestID: "req-2",
			Time:      time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC),
			Model:     "gpt-5",
			URL:       "/v1/responses",
			Method:    "POST",
		},
	}
	events := []RecordEvent{
		{
			Type:    "llm.usage",
			Time:    time.Date(2026, 3, 31, 10, 0, 1, 0, time.UTC),
			Message: "",
			Attributes: map[string]interface{}{
				"prompt_tokens":     float64(11),
				"completion_tokens": float64(7),
				"total_tokens":      float64(18),
			},
		},
	}

	prelude, err := MarshalPrelude(header, events)
	require.NoError(t, err)

	parsed, err := ParsePrelude(prelude)
	require.NoError(t, err)
	require.Len(t, parsed.Events, 1)
	assert.Equal(t, "llm.usage", parsed.Events[0].Type)
	assert.Equal(t, float64(18), parsed.Events[0].Attributes["total_tokens"])
}

func TestSummarizeHTTPExchangePreservesCorrelationAndBoundsBodies(t *testing.T) {
	header := RecordHeader{
		Version: "LLM_PROXY_V3",
		Meta: MetaData{
			RequestID:       "trace-1",
			RequestAuditID:  "reqaudit-1",
			ResponseID:      "resp-1",
			ConversationID:  "thread-1",
			ClientRequestID: "client-1",
			Time:            time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC),
			Model:           "gpt-5",
			URL:             "/v1/responses",
			Method:          "POST",
			StatusCode:      200,
		},
		Layout: LayoutInfo{
			ReqHeaderLen: int64(len("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n")),
			ReqBodyLen:   int64(len(`{"model":"gpt-5","input":"hello"}`)),
			ResHeaderLen: int64(len("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n")),
			ResBodyLen:   int64(len(`{"id":"resp-1","output_text":"hello world"}`)),
		},
	}
	prelude, err := MarshalPrelude(header, BuildEvents(header))
	require.NoError(t, err)
	content := append(prelude, []byte("POST /v1/responses HTTP/1.1\r\nHost: example.com\r\nContent-Type: application/json\r\n\r\n")...)
	content = append(content, []byte(`{"model":"gpt-5","input":"hello"}`)...)
	content = append(content, '\n')
	content = append(content, []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n")...)
	content = append(content, []byte(`{"id":"resp-1","output_text":"hello world"}`)...)

	summary, err := SummarizeHTTPExchange(content, CassetteSummaryOptions{BodyLimit: 12})
	require.NoError(t, err)

	assert.Equal(t, "reqaudit-1", summary.Header.Meta.RequestAuditID)
	assert.Equal(t, "client-1", summary.Header.Meta.ClientRequestID)
	assert.Equal(t, "POST", summary.Request.Method)
	assert.Equal(t, 200, summary.Response.StatusCode)
	assert.Equal(t, `{"model":"gp`, summary.Request.Body)
	assert.True(t, summary.Request.BodyTruncated)
	assert.True(t, summary.Response.BodyTruncated)
	assert.NotEmpty(t, summary.Response.BodySHA256)
	assert.Len(t, summary.Events, 2)
}
