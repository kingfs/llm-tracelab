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

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

const (
	HeaderLen = recordfile.LegacyHeaderLen
)

type PromptTokenDetails = recordfile.PromptTokenDetails
type UsageInfo = recordfile.UsageInfo
type LayoutInfo = recordfile.LayoutInfo
type MetaData = recordfile.MetaData
type RecordHeader = recordfile.RecordHeader
type RecordEvent = recordfile.RecordEvent

type LogInfo struct {
	File   *os.File
	Path   string
	Header RecordHeader
}

type Recorder struct {
	OutputDir string
	MaskKey   bool
	store     *store.Store
}

func New(outputDir string, maskKey bool, st *store.Store) *Recorder {
	return &Recorder{
		OutputDir: outputDir,
		MaskKey:   maskKey,
		store:     st,
	}
}

func (r *Recorder) PrepareLogFile(req *http.Request, siteURL string) (*LogInfo, error) {
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	modelName := "unknown-model"
	if len(bodyBytes) > 0 {
		var payload struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(bodyBytes, &payload) == nil && payload.Model != "" {
			modelName = payload.Model
		}
	}
	if modelName == "unknown-model" && strings.HasSuffix(req.URL.Path, "/models") {
		modelName = "list_models"
	}

	u, _ := url.Parse(siteURL)
	siteHost := "unknown"
	if u != nil {
		siteHost = u.Host
	}

	now := time.Now()
	dirPath := filepath.Join(
		r.OutputDir,
		siteHost,
		modelName,
		now.Format("2006"),
		now.Format("01"),
		now.Format("02"),
	)
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return nil, err
	}

	fileName := fmt.Sprintf("%s_%d.http", now.Format("20060102_150405"), now.Nanosecond())
	logPath := filepath.Join(dirPath, fileName)

	f, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}

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

	nBody, err := f.Write(bodyBytes)
	if err != nil {
		f.Close()
		return nil, err
	}

	header := RecordHeader{
		Version: "LLM_PROXY_V3",
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

func (r *Recorder) UpdateLogFile(info *LogInfo) error {
	if info.File == nil {
		return nil
	}
	defer info.File.Close()

	payload, err := os.ReadFile(info.Path)
	if err != nil {
		return err
	}

	prelude, err := recordfile.MarshalPrelude(info.Header, recordfile.BuildEvents(info.Header))
	if err != nil {
		return err
	}

	if err := info.File.Truncate(0); err != nil {
		return err
	}
	if _, err := info.File.Seek(0, 0); err != nil {
		return err
	}
	if _, err := info.File.Write(prelude); err != nil {
		return err
	}
	if _, err := info.File.Write(payload); err != nil {
		return err
	}

	if r.store != nil {
		if err := r.store.UpsertLog(info.Path, info.Header); err != nil {
			return err
		}
	}

	return nil
}

func (r *Recorder) WriteMetaFile(path string, meta MetaData) error {
	return nil
}
