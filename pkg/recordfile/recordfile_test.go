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
