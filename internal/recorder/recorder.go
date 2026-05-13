package recorder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/pkg/llm"
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
	Events []RecordEvent
}

type PrepareOptions struct {
	SiteURL                        string
	SelectedUpstreamID             string
	SelectedUpstreamProviderPreset string
	RoutingPolicy                  string
	RoutingScore                   float64
	RoutingCandidateCount          int
	RoutingFailureReason           string
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
	return r.PrepareLogFileWithOptions(req, PrepareOptions{SiteURL: siteURL})
}

func (r *Recorder) PrepareLogFileWithOptions(req *http.Request, opts PrepareOptions) (*LogInfo, error) {
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}
	return r.PrepareLogFileWithOptionsAndBody(req, opts, bodyBytes)
}

func (r *Recorder) PrepareLogFileWithOptionsAndBody(req *http.Request, opts PrepareOptions, bodyBytes []byte) (*LogInfo, error) {
	modelName := "unknown-model"
	if len(bodyBytes) > 0 {
		if parsedReq, err := llm.ParseRequestForPath(req.URL.Path, opts.SiteURL, bodyBytes); err == nil && parsedReq.Model != "" {
			modelName = parsedReq.Model
		} else {
			var payload struct {
				Model string `json:"model"`
			}
			if json.Unmarshal(bodyBytes, &payload) == nil && payload.Model != "" {
				modelName = payload.Model
			}
		}
	}
	if modelName == "unknown-model" && strings.HasSuffix(req.URL.Path, "/models") {
		modelName = "list_models"
	}
	if modelName == "unknown-model" {
		if inferred := llm.ModelFromPath(req.URL.Path); inferred != "" {
			modelName = inferred
		}
	}

	u, _ := url.Parse(opts.SiteURL)
	siteHost := "unknown"
	if u != nil {
		siteHost = u.Host
	}

	now := time.Now()
	semantics := llm.ClassifyHTTPRequest(req, opts.SiteURL)
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

	originalHeaders := map[string]string{}
	if r.MaskKey {
		for _, name := range []string{"Authorization", "api-key", "x-api-key", "x-goog-api-key"} {
			if value := req.Header.Get(name); value != "" {
				originalHeaders[name] = value
				switch name {
				case "Authorization":
					req.Header.Set(name, "Bearer fake-key-logging")
				default:
					req.Header.Set(name, "fake-key-logging")
				}
			}
		}
	}
	reqDump, err := httputil.DumpRequest(req, false)
	if r.MaskKey {
		for name, value := range originalHeaders {
			req.Header.Set(name, value)
		}
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
			RequestID:                      fmt.Sprintf("%d", now.UnixNano()),
			Time:                           now,
			Model:                          modelName,
			Provider:                       semantics.Provider,
			Operation:                      semantics.Operation,
			Endpoint:                       semantics.Endpoint,
			URL:                            req.URL.String(),
			Method:                         req.Method,
			ClientIP:                       req.RemoteAddr,
			SelectedUpstreamID:             opts.SelectedUpstreamID,
			SelectedUpstreamBaseURL:        opts.SiteURL,
			SelectedUpstreamProviderPreset: opts.SelectedUpstreamProviderPreset,
			RoutingPolicy:                  opts.RoutingPolicy,
			RoutingScore:                   opts.RoutingScore,
			RoutingCandidateCount:          opts.RoutingCandidateCount,
			RoutingFailureReason:           opts.RoutingFailureReason,
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

	events := recordfile.BuildEvents(info.Header)
	if len(info.Events) > 0 {
		events = append(events, info.Events...)
	}
	prelude, err := recordfile.MarshalPrelude(info.Header, events)
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
		fullContent := append(append([]byte{}, prelude...), payload...)
		parsed, err := recordfile.ParsePrelude(fullContent)
		if err != nil {
			return err
		}
		grouping, err := store.ExtractGroupingInfo(fullContent, parsed)
		if err != nil {
			return err
		}
		if err := r.store.UpsertLogWithGrouping(info.Path, info.Header, grouping); err != nil {
			return err
		}
		entry, err := r.store.GetByRequestID(info.Header.Meta.RequestID)
		if err != nil {
			slog.Warn("Trace parse job enqueue skipped: indexed trace not found", "request_id", info.Header.Meta.RequestID, "error", err)
		} else if err := r.store.EnqueueParseJob(entry.ID); err != nil {
			slog.Warn("Trace parse job enqueue failed", "trace_id", entry.ID, "error", err)
		}
	}

	return nil
}

func (r *Recorder) WriteMetaFile(path string, meta MetaData) error {
	return nil
}
