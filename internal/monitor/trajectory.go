package monitor

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/trajectory"
	"github.com/kingfs/llm-tracelab/pkg/recordfile"
)

func handleSessionTrajectory(w http.ResponseWriter, r *http.Request, st *store.Store, sessionID string) {
	// Query once: a session still running must not grow while being exported.
	entries, err := st.ListTracesBySession(sessionID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Unable to query session"})
		return
	}
	if len(entries) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Session has no recorded requests"})
		return
	}
	exchanges := make([]trajectory.Exchange, 0, len(entries))
	for _, entry := range entries {
		if r.Context().Err() != nil {
			return
		}
		ex := trajectory.Exchange{TraceID: entry.ID, Time: entry.Header.Meta.Time, Model: entry.Header.Meta.Model, Endpoint: entry.Header.Meta.Endpoint, StatusCode: entry.Header.Meta.StatusCode}
		content, readErr := os.ReadFile(entry.LogPath)
		if readErr != nil {
			ex.Error = "Recorded cassette is missing or unreadable"
		} else {
			parsed, parseErr := recordfile.ParsePrelude(content)
			if parseErr != nil {
				ex.Error = "Recorded cassette prelude is invalid"
			} else {
				_, ex.Request, _, ex.Response = recordfile.ExtractSections(content, parsed)
				ex.Stream = parsed.Header.Layout.IsStream
				if ex.Endpoint == "" {
					ex.Endpoint = parsed.Header.Meta.Endpoint
				}
			}
		}
		exchanges = append(exchanges, ex)
	}
	result, err := trajectory.Build(r.Context(), sessionID, entries[0].SessionSource, exchanges)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	// Marshal before writing headers, so errors cannot produce a partial download.
	data, err := json.Marshal(result)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Unable to serialize trajectory"})
		return
	}
	filename := "session-" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, sessionID) + ".atif.jsonl"
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)+1))
	_, _ = w.Write(append(data, '\n'))
}
