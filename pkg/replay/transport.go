package replay

import (
	"bufio"
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
