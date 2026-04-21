package replay

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

// Transport 实现 http.RoundTripper 接口，用于回放本地 .http 文件
type Transport struct {
	Filename string
}

type SummaryOptions struct {
	BodyLimit int
}

type Summary struct {
	RequestMethod string              `json:"request_method"`
	RequestURL    string              `json:"request_url"`
	Status        string              `json:"status"`
	StatusCode    int                 `json:"status_code"`
	ContentType   string              `json:"content_type,omitempty"`
	Header        map[string][]string `json:"header"`
	Body          string              `json:"body,omitempty"`
	BodyBytes     int                 `json:"body_bytes"`
	BodyTruncated bool                `json:"body_truncated"`
	IsStream      bool                `json:"is_stream"`
}

// NewTransport 创建一个新的回放 Transport
func NewTransport(filename string) *Transport {
	return &Transport{Filename: filename}
}

func ReplayFile(filename string, opts SummaryOptions) (*Summary, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("replay: failed to read file %s: %w", filename, err)
	}

	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		return nil, fmt.Errorf("replay: invalid record prelude: %w", err)
	}

	reqFull, _, resFull, _ := recordfile.ExtractSections(content, parsed)
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(reqFull)))
	if err != nil {
		return nil, fmt.Errorf("replay: failed to parse http request: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(resFull)), req)
	if err != nil {
		return nil, fmt.Errorf("replay: failed to parse http response: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("replay: failed to read response body: %w", err)
	}

	limit := opts.BodyLimit
	if limit <= 0 {
		limit = 4096
	}
	if limit > 20000 {
		limit = 20000
	}

	bodyOut := body
	truncated := false
	if len(bodyOut) > limit {
		bodyOut = bodyOut[:limit]
		truncated = true
	}

	return &Summary{
		RequestMethod: req.Method,
		RequestURL:    req.URL.String(),
		Status:        resp.Status,
		StatusCode:    resp.StatusCode,
		ContentType:   resp.Header.Get("Content-Type"),
		Header:        resp.Header,
		Body:          string(bodyOut),
		BodyBytes:     len(body),
		BodyTruncated: truncated,
		IsStream:      parsed.Header.Layout.IsStream,
	}, nil
}

// RoundTrip 执行请求回放逻辑
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	// 1. 打开录制文件
	f, err := os.Open(t.Filename)
	if err != nil {
		return nil, fmt.Errorf("replay: failed to open file %s: %w", t.Filename, err)
	}

	content, err := os.ReadFile(t.Filename)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("replay: failed to read file %s: %w", t.Filename, err)
	}

	parsed, err := recordfile.ParsePrelude(content)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("replay: invalid record prelude: %w", err)
	}

	respOffset := parsed.PayloadOffset + parsed.Header.Layout.ReqHeaderLen + parsed.Header.Layout.ReqBodyLen + 1

	if _, err := f.Seek(respOffset, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("replay: seek failed: %w", err)
	}

	bufReader := bufio.NewReader(f)
	resp, err := http.ReadResponse(bufReader, req)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("replay: failed to parse http response: %w", err)
	}

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
