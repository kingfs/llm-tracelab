package codexfixtures

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
)

const (
	expectedResponseSuffix = "_expected_response.json"
	expectedErrorSuffix    = "_expected_error.json"
	requestSuffix          = "_request.json"
	streamEventsSuffix     = "_events.ndjson"
)

type File struct {
	Name string
	Path string
	Kind string
}

type ExpectedErrorFixture struct {
	RequestExamples []protocol.CreateResponseRequest `json:"request_examples"`
	ExpectedError   protocol.ErrorResponse           `json:"expected_error"`
	Notes           []string                         `json:"notes"`
}

func Dir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("cannot resolve codex fixture helper path")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "tests", "fixtures", "codex")
}

func Path(name string) string {
	return filepath.Join(Dir(), name)
}

func List() ([]File, error) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		return nil, fmt.Errorf("read codex fixtures dir: %w", err)
	}

	files := make([]File, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			return nil, fmt.Errorf("unexpected directory in codex fixtures: %s", entry.Name())
		}
		name := entry.Name()
		if name == "README.md" {
			continue
		}
		kind, ok := fileKind(name)
		if !ok {
			return nil, fmt.Errorf("unexpected codex fixture file %q: want .json or .ndjson", name)
		}
		files = append(files, File{
			Name: name,
			Path: Path(name),
			Kind: kind,
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})
	if len(files) == 0 {
		return nil, fmt.Errorf("no codex fixture files found")
	}
	return files, nil
}

func Names() ([]string, error) {
	files, err := List()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(files))
	for i, file := range files {
		names[i] = file.Name
	}
	return names, nil
}

func Load(name string) ([]byte, error) {
	data, err := os.ReadFile(Path(name))
	if err != nil {
		return nil, fmt.Errorf("read codex fixture %s: %w", name, err)
	}
	return data, nil
}

func Decode[T any](name string) (T, error) {
	var out T
	data, err := Load(name)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("decode codex fixture %s: %w", name, err)
	}
	return out, nil
}

func RequestNames() ([]string, error) {
	files, err := List()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for _, file := range files {
		if file.Kind == "request" {
			names = append(names, file.Name)
		}
	}
	return names, nil
}

func CreateRequestNames() ([]string, error) {
	names, err := RequestNames()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		req, err := Decode[protocol.CreateResponseRequest](name)
		if err != nil {
			return nil, err
		}
		if req.PreviousResponseID == "" {
			out = append(out, name)
		}
	}
	return out, nil
}

func ValidateAll() error {
	files, err := List()
	if err != nil {
		return err
	}

	seen := make(map[string]File, len(files))
	for _, file := range files {
		seen[file.Name] = file
		if err := validateFile(file); err != nil {
			return err
		}
	}

	if err := requireFixturePair(seen, "text_create_request.json", "text_create_expected_response.json"); err != nil {
		return err
	}
	if err := requireFixturePair(seen, "function_call_request.json", "function_call_expected_response.json"); err != nil {
		return err
	}
	if err := requireFixturePair(seen, "stream_text_request.json", "stream_text_events.ndjson"); err != nil {
		return err
	}
	if _, ok := seen["unsupported_hosted_tool_expected_error.json"]; !ok {
		return fmt.Errorf("missing unsupported hosted tool expected error fixture")
	}

	for _, file := range files {
		switch file.Kind {
		case "request":
			if _, err := Decode[protocol.CreateResponseRequest](file.Name); err != nil {
				return err
			}
		case "expected_response":
			resp, err := Decode[protocol.Response](file.Name)
			if err != nil {
				return err
			}
			if resp.ID == "" || resp.Object != "response" || resp.Status == "" || resp.Model == "" {
				return fmt.Errorf("%s: expected response must include id, object=response, status, and model", file.Name)
			}
		case "expected_error":
			fx, err := Decode[ExpectedErrorFixture](file.Name)
			if err != nil {
				return err
			}
			if len(fx.RequestExamples) == 0 {
				return fmt.Errorf("%s: expected error fixture must include request_examples", file.Name)
			}
			if fx.ExpectedError.Error.Type == "" || fx.ExpectedError.Error.Code == "" || fx.ExpectedError.Error.Message == "" {
				return fmt.Errorf("%s: expected_error must include message, type, and code", file.Name)
			}
			for i, req := range fx.RequestExamples {
				if req.Model == "" || req.Input == nil || len(req.Tools) == 0 {
					return fmt.Errorf("%s: request_examples[%d] must include model, input, and tools", file.Name, i)
				}
			}
		case "stream_events":
			if err := validateStreamEvents(file.Name); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: unhandled fixture kind %q", file.Name, file.Kind)
		}
	}

	return nil
}

func fileKind(name string) (string, bool) {
	switch {
	case strings.HasSuffix(name, expectedResponseSuffix):
		return "expected_response", true
	case strings.HasSuffix(name, expectedErrorSuffix):
		return "expected_error", true
	case strings.HasSuffix(name, requestSuffix):
		return "request", true
	case strings.HasSuffix(name, streamEventsSuffix):
		return "stream_events", true
	case strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".ndjson"):
		return "", false
	default:
		return "", false
	}
}

func validateFile(file File) error {
	data, err := os.ReadFile(file.Path)
	if err != nil {
		return fmt.Errorf("read codex fixture %s: %w", file.Name, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return fmt.Errorf("%s: fixture must not be empty", file.Name)
	}
	if strings.HasSuffix(file.Name, ".json") && !json.Valid(data) {
		return fmt.Errorf("%s: invalid JSON", file.Name)
	}
	return nil
}

func validateStreamEvents(name string) error {
	data, err := Load(name)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNumber := 0
	var completed bool
	for scanner.Scan() {
		lineNumber++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			return fmt.Errorf("%s:%d: stream fixture must not contain blank lines", name, lineNumber)
		}
		var event protocol.StreamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("%s:%d: decode stream event: %w", name, lineNumber, err)
		}
		if event.Type == "" {
			return fmt.Errorf("%s:%d: stream event type is required", name, lineNumber)
		}
		if event.Type == "response.completed" {
			completed = true
			if event.Response == nil || event.Response.ID == "" || event.Response.Status != "completed" {
				return fmt.Errorf("%s:%d: response.completed must include completed response", name, lineNumber)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan stream fixture %s: %w", name, err)
	}
	if lineNumber == 0 {
		return fmt.Errorf("%s: stream fixture must contain at least one event", name)
	}
	if !completed {
		return fmt.Errorf("%s: stream fixture must include response.completed", name)
	}
	return nil
}

func requireFixturePair(seen map[string]File, requestName, expectedName string) error {
	if _, ok := seen[requestName]; !ok {
		return fmt.Errorf("missing request fixture %s", requestName)
	}
	if _, ok := seen[expectedName]; !ok {
		return fmt.Errorf("missing expected fixture %s for %s", expectedName, requestName)
	}
	return nil
}
