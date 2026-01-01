package monitor

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/recorder"
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

// LogEntry 用于 List 页面
type LogEntry struct {
	recorder.RecordHeader
	LogPath string
}

type ListData struct {
	Logs  []LogEntry
	Stats LogStats
}

func ScanLogs(root string) (ListData, error) {
	var entries []LogEntry
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".http") {
			// 只读第一行
			f, _ := os.Open(path)
			if f != nil {
				r := bufio.NewReader(f)
				line, _ := r.ReadString('\n')
				f.Close()

				var header recorder.RecordHeader
				if json.Unmarshal([]byte(line), &header) == nil {
					entries = append(entries, LogEntry{
						RecordHeader: header,
						LogPath:      path,
					})
				}
			}
		}
		return nil
	})

	totalRequests := len(entries)
	success := 0
	failed := 0
	totalToken := 0
	totalTTFT := 0
	for _, e := range entries {
		if e.Meta.StatusCode > 199 && e.Meta.StatusCode < 300 {
			totalToken += e.Usage.TotalTokens
			totalTTFT += int(e.Meta.TTFTMs)
			success++
		} else {
			failed++
		}
	}

	successRate := 0.0
	if success+failed > 0 {
		successRate = float64(success) / float64(success+failed)
	}

	avgTTFT := 0
	if totalTTFT > 0 && success > 0 {
		avgTTFT = totalTTFT / success
	}

	stats := LogStats{
		TotalRequest:   totalRequests,
		SuccessRequest: success,
		FailedRequest:  failed,
		SuccessRate:    successRate,
		AvgTTFT:        avgTTFT,
		TotalTokens:    totalToken,
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Meta.Time.After(entries[j].Meta.Time)
	})
	if len(entries) > 500 {
		entries = entries[:500]
	}

	return ListData{
		Logs:  entries,
		Stats: stats,
	}, nil
}

func ListHandler(outputDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, _ := ScanLogs(outputDir)

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
			"len":    func(s string) int { return len(s) }, // 注册 len 函数
			// [新增] 用于美化 JSON 字符串 (如 arguments: "{\"a\":1}")
			"prettyJsonString": func(str string) string {
				var v interface{}
				if err := json.Unmarshal([]byte(str), &v); err == nil {
					b, _ := json.MarshalIndent(v, "", "  ")
					return string(b)
				}
				return str // 解析失败则原样返回
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

// DownloadHandler 提供原始 .http 文件下载
func DownloadHandler(outputDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path == "" {
			http.Error(w, "missing path", 400)
			return
		}

		// 安全检查：防止路径穿越
		cleanPath := filepath.Clean(path)
		absOutput, _ := filepath.Abs(outputDir)
		absPath, _ := filepath.Abs(cleanPath)

		if !strings.HasPrefix(absPath, absOutput) {
			http.Error(w, "forbidden: invalid path", 403)
			return
		}

		// 检查文件是否存在
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			http.Error(w, "file not found", 404)
			return
		}

		// 设置下载头
		filename := filepath.Base(absPath)
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		w.Header().Set("Content-Type", "application/octet-stream")

		// 使用 http.ServeFile 高效传输
		http.ServeFile(w, r, absPath)
	}
}
