package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunConvertsV2AndRebuildsIndex(t *testing.T) {
	dir := t.TempDir()
	v2Path := filepath.Join(dir, "legacy.http")
	payload := []byte("POST /v1/chat/completions HTTP/1.1\r\nHost: example.com\r\n\r\n{\"model\":\"gpt-4.1\"}\nHTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"id\":\"resp_1\"}")
	writeLegacyRecord(t, v2Path, payload)

	st, err := store.New(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	result, err := Run(st, Options{
		OutputDir: dir,
		RewriteV2: true,
		RebuildDB: true,
	})
	require.NoError(t, err)

	assert.Equal(t, 1, result.ScannedFiles)
	assert.Equal(t, 1, result.ConvertedFiles)
	assert.Equal(t, 1, result.RebuiltIndexRows)

	content, err := os.ReadFile(v2Path)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(content), recordfile.FileMagic))

	parsed, err := recordfile.ParsePrelude(content)
	require.NoError(t, err)
	assert.Equal(t, "LLM_PROXY_V3", parsed.Header.Version)

	entries, err := st.ListRecent(10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "LLM_PROXY_V3", entries[0].Header.Version)
}

func writeLegacyRecord(t *testing.T, path string, payload []byte) {
	t.Helper()

	header := recordfile.RecordHeader{
		Version: "LLM_PROXY_V2",
		Meta: recordfile.MetaData{
			RequestID:     "req-legacy",
			Time:          time.Date(2025, 12, 1, 8, 0, 0, 0, time.UTC),
			Model:         "gpt-4.1",
			URL:           "/v1/chat/completions",
			Method:        "POST",
			StatusCode:    200,
			DurationMs:    321,
			TTFTMs:        87,
			ClientIP:      "127.0.0.1:12345",
			ContentLength: int64(len(payload)),
		},
		Layout: recordfile.LayoutInfo{
			ReqHeaderLen: 61,
			ReqBodyLen:   19,
			ResHeaderLen: 51,
			ResBodyLen:   15,
			IsStream:     false,
		},
		Usage: recordfile.UsageInfo{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	headerJSON, err := json.Marshal(header)
	require.NoError(t, err)
	require.Less(t, len(headerJSON)+1, recordfile.LegacyHeaderLen)

	buf := make([]byte, recordfile.LegacyHeaderLen)
	copy(buf, append(headerJSON, '\n'))
	buf = append(buf, payload...)

	err = os.WriteFile(path, buf, 0o644)
	require.NoError(t, err)
}
