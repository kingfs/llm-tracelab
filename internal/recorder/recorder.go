package recorder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// HeaderLen 定义头部 JSON 的固定长度 (2KB)，方便 Seek 和计算偏移
	// 必须足够大以容纳 MetaData + Layout + Usage
	HeaderLen = 2048
)

type PromptTokenDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// UsageInfo 存储 Token 消耗
type UsageInfo struct {
	PromptTokens       int                 `json:"prompt_tokens"`
	CompletionTokens   int                 `json:"completion_tokens"`
	TotalTokens        int                 `json:"total_tokens"`
	PromptTokenDetails *PromptTokenDetails `json:"prompt_tokens_details,omitempty"`
}

// LayoutInfo 定义文件内部各部分的字节长度
type LayoutInfo struct {
	ReqHeaderLen int64 `json:"req_header_len"`
	ReqBodyLen   int64 `json:"req_body_len"`
	// ReqBody 和 ResHeader 之间有一个显式的 \n 分隔符，固定为 1 字节，不在此记录
	ResHeaderLen int64 `json:"res_header_len"`
	ResBodyLen   int64 `json:"res_body_len"`
	IsStream     bool  `json:"is_stream"`
}

// MetaData 基础元数据
type MetaData struct {
	RequestID     string    `json:"request_id"`
	Time          time.Time `json:"time"`
	Model         string    `json:"model"`
	URL           string    `json:"url"`
	Method        string    `json:"method"`
	StatusCode    int       `json:"status_code"`
	DurationMs    int64     `json:"duration_ms"`
	TTFTMs        int64     `json:"ttft_ms"`
	ClientIP      string    `json:"client_ip"`
	ContentLength int64     `json:"content_length"`
	Error         string    `json:"error,omitempty"`
}

// RecordHeader 是存储在文件第一行的完整元数据 (JSON)
type RecordHeader struct {
	Version string     `json:"version"` // "LLM_PROXY_V2"
	Meta    MetaData   `json:"meta"`
	Layout  LayoutInfo `json:"layout"`
	Usage   UsageInfo  `json:"usage"`
}

// LogInfo 在 Context 中传递的对象
type LogInfo struct {
	File   *os.File
	Path   string
	Header RecordHeader // 内存中维护的 Header 状态，最后 Update 时写入文件
}

type Recorder struct {
	OutputDir string
	MaskKey   bool
}

func New(outputDir string, maskKey bool) *Recorder {
	return &Recorder{
		OutputDir: outputDir,
		MaskKey:   maskKey,
	}
}

// PrepareLogFile 初始化文件，写入 2KB 占位符
func (r *Recorder) PrepareLogFile(req *http.Request, siteURL string) (*LogInfo, error) {
	// 1. 读取 Body 获取 Model 名称
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		// 重置 Body 供后续使用
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	modelName := "unknown-model"
	if len(bodyBytes) > 0 {
		var pb struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(bodyBytes, &pb) == nil && pb.Model != "" {
			modelName = pb.Model
		}
	}
	// 补救逻辑
	if modelName == "unknown-model" {
		if strings.HasSuffix(req.URL.Path, "/models") {
			modelName = "list_models"
		}
	}

	// 2. 解析 Site Host
	u, _ := url.Parse(siteURL)
	siteHost := "unknown"
	if u != nil {
		siteHost = u.Host
	}

	// 3. 创建目录和文件
	now := time.Now()
	dirPath := filepath.Join(
		r.OutputDir,
		siteHost,
		modelName,
		now.Format("2006"),
		now.Format("01"),
		now.Format("02"),
	)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		return nil, err
	}
	fileName := fmt.Sprintf("%s_%d.http", now.Format("20060102_150405"), now.Nanosecond())
	logPath := filepath.Join(dirPath, fileName)

	f, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}

	// 4. [关键] 写入 2KB 占位符 (全空格，最后为换行)
	padding := make([]byte, HeaderLen)
	for i := range padding {
		padding[i] = ' '
	}
	padding[HeaderLen-1] = '\n'
	if _, err := f.Write(padding); err != nil {
		f.Close()
		return nil, err
	}

	// 5. 写入 Request Header
	originalKey := req.Header.Get("Authorization")
	if r.MaskKey && originalKey != "" {
		req.Header.Set("Authorization", "Bearer fake-key-logging")
	}
	reqDump, err := httputil.DumpRequest(req, false)
	if r.MaskKey && originalKey != "" {
		req.Header.Set("Authorization", originalKey)
	}
	if err != nil {
		f.Close()
		return nil, err
	}

	nHead, err := f.Write(reqDump)
	if err != nil {
		f.Close()
		return nil, err
	}

	// 6. 写入 Request Body
	nBody, err := f.Write(bodyBytes)
	if err != nil {
		f.Close()
		return nil, err
	}

	// 初始化 Header 结构
	header := RecordHeader{
		Version: "LLM_PROXY_V2",
		Meta: MetaData{
			RequestID: fmt.Sprintf("%d", now.UnixNano()),
			Time:      now,
			Model:     modelName,
			URL:       req.URL.String(),
			Method:    req.Method,
			ClientIP:  req.RemoteAddr,
		},
		Layout: LayoutInfo{
			ReqHeaderLen: int64(nHead),
			ReqBodyLen:   int64(nBody),
		},
	}

	return &LogInfo{
		File:   f,
		Path:   logPath,
		Header: header,
	}, nil
}

// UpdateLogFile 请求结束时调用，回填 Header JSON
func (r *Recorder) UpdateLogFile(info *LogInfo) error {
	if info.File == nil {
		return nil
	}
	defer info.File.Close()

	// 1. 序列化 Header
	jsonData, err := json.Marshal(info.Header)
	if err != nil {
		return err
	}

	// 2. 准备填充块 (2KB)
	finalBlock := make([]byte, HeaderLen)
	for i := range finalBlock {
		finalBlock[i] = ' '
	}

	// 3. 复制 JSON 到块中
	// 如果 JSON 过长 (极少情况)，进行截断，保留前部数据防止错位
	if len(jsonData) > HeaderLen-1 {
		copy(finalBlock, jsonData[:HeaderLen-1])
	} else {
		copy(finalBlock, jsonData)
	}

	// 4. 确保最后是换行符
	finalBlock[HeaderLen-1] = '\n'

	// 5. Seek 到头部并覆盖
	if _, err := info.File.Seek(0, 0); err != nil {
		return err
	}
	if _, err := info.File.Write(finalBlock); err != nil {
		return err
	}

	return nil
}

// WriteMetaFile V2 模式下，Meta 信息已包含在文件头，不再需要单独的 .meta.json 文件
// 但为了兼容旧逻辑接口，这里可以留空或删除。
// 建议删除，因为所有信息都在 .http 文件里了。
func (r *Recorder) WriteMetaFile(path string, meta MetaData) error {
	return nil // V2 不再生成单独的 meta 文件
}
