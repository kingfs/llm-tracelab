package monitor

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

type LogStats struct {
	TotalRequest   int
	AvgTTFT        int
	TotalTokens    int
	SuccessRequest int
	FailedRequest  int
	SuccessRate    float64
}

type LogEntry = store.LogEntry

type ListData struct {
	Logs  []LogEntry
	Stats LogStats
}

type rawDetailResponse struct {
	RequestProtocol  string            `json:"request_protocol"`
	ResponseProtocol string            `json:"response_protocol"`
	Header           recordHeaderView  `json:"header"`
	Events           []recordEventView `json:"events"`
}

type recordHeaderView struct {
	Version string      `json:"version"`
	Meta    interface{} `json:"meta"`
	Layout  interface{} `json:"layout"`
	Usage   interface{} `json:"usage"`
}

type recordEventView map[string]interface{}

func ListHandler(outputDir string, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = outputDir
		if st == nil {
			http.Error(w, "store not configured", 500)
			return
		}
		if err := st.Sync(); err != nil {
			http.Error(w, "sync error: "+err.Error(), 500)
			return
		}

		logs, err := st.ListRecent(500)
		if err != nil {
			http.Error(w, "query error: "+err.Error(), 500)
			return
		}
		stats, err := st.Stats()
		if err != nil {
			http.Error(w, "stats error: "+err.Error(), 500)
			return
		}

		data := ListData{
			Logs: logs,
			Stats: LogStats{
				TotalRequest:   stats.TotalRequest,
				AvgTTFT:        stats.AvgTTFT,
				TotalTokens:    stats.TotalTokens,
				SuccessRequest: stats.SuccessRequest,
				FailedRequest:  stats.FailedRequest,
				SuccessRate:    stats.SuccessRate,
			},
		}

		tmpl, err := template.ParseFS(templatesFS, "templates/list.html")
		if err != nil {
			slog.Error("Template parse error", "err", err)
			http.Error(w, "Template Error", 500)
			return
		}
		tmpl.Execute(w, data)
	}
}

func DetailHandler(outputDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			http.Error(w, "missing path", 400)
			return
		}

		absPath, err := resolveTracePath(outputDir, path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		content, err := os.ReadFile(absPath)
		if err != nil {
			http.Error(w, "file not found", 404)
			return
		}

		parsed, err := ParseLogFile(content)
		if err != nil {
			http.Error(w, "Parse Error: "+err.Error(), 500)
			return
		}

		funcMap := template.FuncMap{
			"fmtdur": func(ms int64) string { return (time.Duration(ms) * time.Millisecond).String() },
			"json":   func(v interface{}) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) },
			"prettyJsonString": func(str string) string {
				var v interface{}
				if err := json.Unmarshal([]byte(str), &v); err == nil {
					b, _ := json.MarshalIndent(v, "", "  ")
					return string(b)
				}
				return str
			},
		}

		tmpl, err := template.New("detail.html").Funcs(funcMap).ParseFS(templatesFS, "templates/detail.html")
		if err != nil {
			slog.Error("Template parse error", "err", err)
			http.Error(w, "Template Error: "+err.Error(), 500)
			return
		}
		tmpl.Execute(w, parsed)
	}
}

func DetailRawHandler(outputDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			http.Error(w, "missing path", http.StatusBadRequest)
			return
		}

		absPath, err := resolveTracePath(outputDir, path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		content, err := os.ReadFile(absPath)
		if err != nil {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}

		parsed, err := ParseLogFile(content)
		if err != nil {
			http.Error(w, "Parse Error: "+err.Error(), http.StatusInternalServerError)
			return
		}

		payload := rawDetailResponse{
			RequestProtocol:  parsed.ReqFull,
			ResponseProtocol: parsed.ResFull,
			Header: recordHeaderView{
				Version: parsed.Header.Version,
				Meta:    parsed.Header.Meta,
				Layout:  parsed.Header.Layout,
				Usage:   parsed.Header.Usage,
			},
		}
		if len(parsed.Events) > 0 {
			payload.Events = make([]recordEventView, 0, len(parsed.Events))
			for _, event := range parsed.Events {
				row := recordEventView{
					"type": event.Type,
					"time": event.Time,
				}
				if event.Method != "" {
					row["method"] = event.Method
				}
				if event.URL != "" {
					row["url"] = event.URL
				}
				if event.StatusCode != 0 {
					row["status_code"] = event.StatusCode
				}
				if event.IsStream {
					row["is_stream"] = event.IsStream
				}
				if event.HeaderBytes != 0 {
					row["header_bytes"] = event.HeaderBytes
				}
				if event.BodyBytes != 0 {
					row["body_bytes"] = event.BodyBytes
				}
				payload.Events = append(payload.Events, row)
			}
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			http.Error(w, "encode error: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func DownloadHandler(outputDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			http.Error(w, "missing path", 400)
			return
		}

		absPath, err := resolveTracePath(outputDir, path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		filename := filepath.Base(absPath)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeFile(w, r, absPath)
	}
}

func resolveTracePath(outputDir, path string) (string, error) {
	cleanPath := filepath.Clean(path)
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return "", fmt.Errorf("invalid output dir")
	}
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("invalid path")
	}

	if absPath != absOutput && !strings.HasPrefix(absPath, absOutput+string(os.PathSeparator)) {
		return "", fmt.Errorf("forbidden: invalid path")
	}
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found")
	}
	return absPath, nil
}
