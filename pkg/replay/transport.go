package replay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

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
	// 恢复使用普通 fileCloser
	fc := &fileCloser{
		ReadCloser: resp.Body,
		File:       f,
	}
	resp.Body = fc

	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body = &delayedSSEReader{
			reader:   fc,
			minDelay: 10 * time.Millisecond,
			maxDelay: 30 * time.Millisecond, // 降低最大延迟，避免太慢
		}
	}

	return resp, nil
}

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

type delayedSSEReader struct {
	reader   io.ReadCloser
	minDelay time.Duration
	maxDelay time.Duration
	buf      []byte
}

func (r *delayedSSEReader) Read(p []byte) (n int, err error) {
	if len(r.buf) == 0 {
		tmp := make([]byte, 4096)
		n, err := r.reader.Read(tmp)
		if n > 0 {
			r.buf = append(r.buf, tmp[:n]...)
		}
		if err != nil && err != io.EOF {
			return 0, err
		}
		if n == 0 && err == io.EOF {
			return 0, io.EOF
		}
	}

	if len(r.buf) == 0 {
		return 0, io.EOF
	}

	idx := bytes.IndexByte(r.buf, '\n')
	if idx >= 0 {
		toCopy := idx + 1
		shouldSleep := true
		if toCopy > len(p) {
			toCopy = len(p)
			shouldSleep = false
		}
		copy(p, r.buf[:toCopy])
		r.buf = r.buf[toCopy:]

		if shouldSleep {
			// 跳过空行或非 data 行的延迟，避免无效等待
			if toCopy > 1 && len(r.buf) > 0 { // 简单的启发式：如果刚读完一行且还有数据，且刚读完的不是空行
				trimmed := bytes.TrimSpace(p[:toCopy])
				if len(trimmed) > 0 {
					// Random delay between minDelay and maxDelay
					delta := int64(r.maxDelay - r.minDelay)
					if delta > 0 {
						sleepTime := r.minDelay + time.Duration(rand.Int63n(delta))
						time.Sleep(sleepTime)
					} else {
						time.Sleep(r.minDelay)
					}
				}
			}
		}
		return toCopy, nil
	}

	toCopy := len(r.buf)
	if toCopy > len(p) {
		toCopy = len(p)
	}
	copy(p, r.buf[:toCopy])
	r.buf = r.buf[toCopy:]
	return toCopy, nil
}

func (r *delayedSSEReader) Close() error {
	return r.reader.Close()
}
