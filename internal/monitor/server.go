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

		content, err := os.ReadFile(path)
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
			"len":    func(s string) int { return len(s) },
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

func DownloadHandler(outputDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			http.Error(w, "missing path", 400)
			return
		}

		cleanPath := filepath.Clean(path)
		absOutput, _ := filepath.Abs(outputDir)
		absPath, _ := filepath.Abs(cleanPath)

		if !strings.HasPrefix(absPath, absOutput) {
			http.Error(w, "forbidden: invalid path", 403)
			return
		}
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			http.Error(w, "file not found", 404)
			return
		}

		filename := filepath.Base(absPath)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeFile(w, r, absPath)
	}
}
