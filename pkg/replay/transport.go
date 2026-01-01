package replay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const (
	// HeaderLen 必须与 Recorder 中定义的长度保持一致 (2KB)
	HeaderLen = 2048
)

// fileHeader 定义文件头部的 JSON 结构（仅需布局信息）
type fileHeader struct {
	Version string `json:"version"`
	Layout  struct {
		ReqHeaderLen int64 `json:"req_header_len"`
		ReqBodyLen   int64 `json:"req_body_len"`
		ResHeaderLen int64 `json:"res_header_len"`
		ResBodyLen   int64 `json:"res_body_len"`
	} `json:"layout"`
}

// Transport 实现 http.RoundTripper 接口，用于回放本地 .http 文件
type Transport struct {
	Filename string
}

// NewTransport 创建一个新的回放 Transport
func NewTransport(filename string) *Transport {
	return &Transport{Filename: filename}
}

// RoundTrip 执行请求回放逻辑
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 1. 打开录制文件
	f, err := os.Open(t.Filename)
	if err != nil {
		return nil, fmt.Errorf("replay: failed to open file %s: %w", t.Filename, err)
	}

	// 2. 读取并解析 Header JSON (前 2KB)
	headerData := make([]byte, HeaderLen)
	if _, err := io.ReadFull(f, headerData); err != nil {
		f.Close()
		return nil, fmt.Errorf("replay: failed to read header block: %w", err)
	}

	// 截取有效 JSON (换行符之前的部分)
	idx := bytes.IndexByte(headerData, '\n')
	if idx == -1 {
		f.Close()
		return nil, fmt.Errorf("replay: invalid header format (no newline found)")
	}

	var meta fileHeader
	if err := json.Unmarshal(headerData[:idx], &meta); err != nil {
		f.Close()
		return nil, fmt.Errorf("replay: invalid header json: %w", err)
	}

	// 3. 计算 Response 的起始位置
	// V2 格式: [Header 2048] [ReqHeader] [ReqBody] [\n] [ResHeader] [ResBody]
	// 偏移量 = HeaderLen + ReqH + ReqB + 1
	respOffset := int64(HeaderLen) + meta.Layout.ReqHeaderLen + meta.Layout.ReqBodyLen + 1

	// 4. Seek 到 Response 处
	if _, err := f.Seek(respOffset, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("replay: seek failed: %w", err)
	}

	// 5. 解析 HTTP Response
	// http.ReadResponse 需要一个 bufio.Reader
	// 它会自动解析 Status Line, Headers，并准备好 Body 的读取器
	bufReader := bufio.NewReader(f)
	resp, err := http.ReadResponse(bufReader, req)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("replay: failed to parse http response: %w", err)
	}

	// 6. 接管 Body 关闭逻辑
	// 当调用者关闭 resp.Body 时，我们需要同时关闭底层的 os.File
	resp.Body = &fileCloser{
		ReadCloser: resp.Body,
		File:       f,
	}

	return resp, nil
}

// fileCloser 包装器，确保 Body 关闭时文件句柄也被释放
type fileCloser struct {
	io.ReadCloser
	File *os.File
}

func (fc *fileCloser) Close() error {
	// 先关 Body (虽然 http.Response.Body 通常是基于 bufio 的 wrapper，不持有 fd)
	err1 := fc.ReadCloser.Close()
	// 再关实际的文件句柄
	err2 := fc.File.Close()

	if err1 != nil {
		return err1
	}
	return err2
}
