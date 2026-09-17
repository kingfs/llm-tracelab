package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kingfs/llm-tracelab/internal/store"
	"github.com/kingfs/llm-tracelab/internal/trajectory"
)

func TestSessionTrajectoryDownload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.http")
	content := buildRecordFixtureWithRequestHeaders(t, "/v1/responses", false, []string{"Session-Id: export-session"}, `{"input":"hello"}`, `{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"world"}]}]}`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	syncStore(t, st)
	request := func(method, id string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		sessionDetailAPIHandler(st).ServeHTTP(rr, httptest.NewRequest(method, "/api/sessions/"+id+"/trajectory", nil))
		return rr
	}
	rr := request(http.MethodGet, "export-session")
	if rr.Code != 200 {
		t.Fatal(rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("Content-Disposition"), ".atif.jsonl") || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(rr.Header())
	}
	if strings.Count(rr.Body.String(), "\n") != 1 {
		t.Fatal("not one JSONL record")
	}
	var result trajectory.Trajectory
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != trajectory.SchemaVersion || len(result.Steps) != 2 || result.Steps[1].Message != "world" {
		t.Fatalf("%+v", result)
	}
	if got := request(http.MethodPost, "export-session"); got.Code != http.StatusMethodNotAllowed {
		t.Fatal(got.Code)
	}
	if got := request(http.MethodGet, "absent"); got.Code != http.StatusNotFound {
		t.Fatal(got.Code)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(content) {
		t.Fatal("export changed source cassette")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	rr = request(http.MethodGet, "export-session")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "cassette_unavailable") {
		t.Fatal("missing cassette not reported", rr.Code, rr.Body.String())
	}
}
