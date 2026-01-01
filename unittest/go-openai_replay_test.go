package unittest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/pkg/replay"

	"github.com/sashabaranov/go-openai"
)

// newTestClient 是一个辅助函数，用于封装 Transport 和 OpenAI Client 的构建逻辑
// 类似于 httprr 中的 newGeminiTestClientConfig
func newTestClient(t *testing.T, filename string) *openai.Client {
	t.Helper() // 标记为辅助函数，报错时显示调用者的行号

	// 1. 检查文件是否存在，如果不存在则跳过测试
	// 这对协作者很友好，避免因为缺少本地录制文件而导致测试挂红
	path := filepath.Join("testdata", filename)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("Replay file not found: %s, skipping test.", path)
	}

	// 2. 初始化 Replay Transport
	tr := replay.NewTransport(path)

	// 3. 配置 Client
	config := openai.DefaultConfig("fake-api-key")
	config.BaseURL = "http://localhost/v1" // 这里的 URL 不重要，因为会被 Transport 拦截
	config.HTTPClient = &http.Client{
		Transport: tr,
	}

	return openai.NewClientWithConfig(config)
}

// TestChatCompletion_Replay 测试非流式（Non-Stream）请求
func TestChatCompletion_Replay(t *testing.T) {
	tests := []struct {
		name     string
		filename string // 录制文件的名称
		req      openai.ChatCompletionRequest
		want     string // 期望的模型输出内容
		wantErr  bool
	}{
		{
			name:     "basic_non_stream",
			filename: "non-stream.http",
			req: openai.ChatCompletionRequest{
				Model: "qwen3-max",
				Messages: []openai.ChatCompletionMessage{
					{Role: openai.ChatMessageRoleUser, Content: "用5个字介绍一下自己"},
				},
				MaxTokens: 10,
				Stream:    false,
			},
			// 注意：这里的 want 必须和你录制的 non-stream.http 中的实际输出一致
			want:    "AI小助手",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, tt.filename)

			// 执行调用
			resp, err := client.CreateChatCompletion(context.Background(), tt.req)

			// 验证错误状态
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateChatCompletion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			// 验证响应内容
			if len(resp.Choices) == 0 {
				t.Errorf("Got empty choices")
				return
			}
			got := resp.Choices[0].Message.Content

			// 简单的包含匹配或全等匹配
			if !strings.Contains(got, tt.want) {
				t.Errorf("CreateChatCompletion() got = %q, want contain %q", got, tt.want)
			}
		})
	}
}

// TestChatCompletionStream_Replay 测试流式（Stream）请求
func TestChatCompletionStream_Replay(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		req      openai.ChatCompletionRequest
		want     string // 期望拼接后的完整文本
		wantErr  bool
	}{
		{
			name:     "basic_stream",
			filename: "stream.http",
			req: openai.ChatCompletionRequest{
				Model: "qwen3-max",
				Messages: []openai.ChatCompletionMessage{
					{Role: openai.ChatMessageRoleUser, Content: "用5个字介绍一下自己"},
				},
				Stream: true,
			},
			// 注意：这里的 want 必须和你录制的 stream.http 拼接后的文本一致
			want:    "AI小助手",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, tt.filename)

			// 执行 Stream 调用
			stream, err := client.CreateChatCompletionStream(context.Background(), tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateStream() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			defer stream.Close()

			// 接收并拼接 Stream 内容
			var gotBuilder strings.Builder
			for {
				resp, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Errorf("Stream recv error = %v", err)
					return
				}
				if len(resp.Choices) > 0 {
					gotBuilder.WriteString(resp.Choices[0].Delta.Content)
				}
			}

			got := gotBuilder.String()

			// 验证结果
			if !strings.Contains(got, tt.want) {
				t.Errorf("Stream result got = %q, want contain %q", got, tt.want)
			}
		})
	}
}
