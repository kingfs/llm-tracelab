package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	"github.com/kingfs/llm-tracelab/internal/responses/tools/hosted"
)

const maxInlineSummaryBytes = 256

var (
	ErrDisabled       = errors.New("mcp hosted tool executor is disabled")
	ErrMissingTool    = errors.New("mcp hosted tool call is missing tool name")
	ErrToolDenied     = errors.New("mcp hosted tool is denied")
	ErrToolNotAllowed = errors.New("mcp hosted tool is not allowed")
	ErrUnknownServer  = errors.New("mcp hosted server is unknown")
	ErrUnknownTool    = errors.New("mcp hosted tool is unknown")
)

type Options struct {
	Enabled bool
	Servers []ServerDescriptor
}

type ServerDescriptor struct {
	ID           string
	Label        string
	Enabled      bool
	AllowedTools []string
	DeniedTools  []string
	Tools        []ToolDescriptor
}

type ToolDescriptor struct {
	Name             string
	Enabled          bool
	TextResult       string
	StructuredResult any
}

type Descriptor struct {
	Type         string
	ServerLabel  string
	AllowedTools []string
	DeniedTools  []string
}

type Executor struct {
	enabled          bool
	defaultServerKey string
	servers          map[string]serverRuntime
}

type serverRuntime struct {
	id      string
	label   string
	enabled bool
	allow   map[string]struct{}
	deny    map[string]struct{}
	tools   map[string]ToolDescriptor
}

type callInput struct {
	ServerID    string          `json:"server_id,omitempty"`
	ServerLabel string          `json:"server_label,omitempty"`
	Tool        string          `json:"tool,omitempty"`
	Name        string          `json:"name,omitempty"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
}

type Output struct {
	ServerID         string         `json:"server_id,omitempty"`
	ServerLabel      string         `json:"server_label,omitempty"`
	ToolName         string         `json:"tool_name"`
	Text             string         `json:"text,omitempty"`
	StructuredResult any            `json:"structured_result,omitempty"`
	Arguments        map[string]any `json:"arguments,omitempty"`
}

func NewExecutor(opts Options) *Executor {
	e := &Executor{
		enabled: opts.Enabled,
		servers: map[string]serverRuntime{},
	}
	for _, server := range opts.Servers {
		runtime := serverRuntime{
			id:      strings.TrimSpace(server.ID),
			label:   strings.TrimSpace(server.Label),
			enabled: server.Enabled,
			allow:   stringSet(server.AllowedTools),
			deny:    stringSet(server.DeniedTools),
			tools:   map[string]ToolDescriptor{},
		}
		for _, tool := range server.Tools {
			name := normalizeToolName(tool.Name)
			if name == "" {
				continue
			}
			tool.Name = name
			runtime.tools[name] = tool
		}
		if runtime.id != "" {
			e.servers[runtime.id] = runtime
		}
		if runtime.label != "" {
			e.servers[runtime.label] = runtime
		}
	}
	if len(opts.Servers) == 1 {
		e.defaultServerKey = firstNonEmpty(opts.Servers[0].Label, opts.Servers[0].ID)
	}
	return e
}

func (e *Executor) Type() string {
	return hosted.ToolTypeMCP
}

func (e *Executor) Enabled(hosted.ToolContext) bool {
	return e != nil && e.enabled
}

func (e *Executor) ExecuteHostedTool(ctx context.Context, toolCtx hosted.ToolContext, call hosted.ToolCall) (hosted.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return hosted.ToolResult{}, err
	}
	if !e.Enabled(toolCtx) {
		return hosted.ToolResult{}, ErrDisabled
	}

	descriptor := DescriptorFromProtocolTool(call.Tool)
	input, args, err := parseCallInput(call)
	if err != nil {
		return hosted.ToolResult{}, err
	}
	serverKey := firstNonEmpty(input.ServerLabel, input.ServerID, descriptor.ServerLabel)
	toolName := firstNonEmpty(input.Tool, input.Name, call.Name)
	toolName = normalizeToolName(toolName)
	if toolName == "" {
		return hosted.ToolResult{}, ErrMissingTool
	}

	server, err := e.resolveServer(serverKey)
	if err != nil {
		return hosted.ToolResult{}, err
	}
	if !server.enabled {
		return hosted.ToolResult{}, ErrDisabled
	}
	if contains(server.deny, toolName) || contains(stringSet(descriptor.DeniedTools), toolName) {
		return hosted.ToolResult{}, ErrToolDenied
	}
	if len(server.allow) > 0 && !contains(server.allow, toolName) {
		return hosted.ToolResult{}, ErrToolNotAllowed
	}
	if len(descriptor.AllowedTools) > 0 && !contains(stringSet(descriptor.AllowedTools), toolName) {
		return hosted.ToolResult{}, ErrToolNotAllowed
	}

	tool, ok := server.tools[toolName]
	if !ok {
		return hosted.ToolResult{}, ErrUnknownTool
	}
	if !tool.Enabled {
		return hosted.ToolResult{}, ErrDisabled
	}

	output := Output{
		ServerID:         server.id,
		ServerLabel:      server.label,
		ToolName:         toolName,
		Text:             tool.TextResult,
		StructuredResult: tool.StructuredResult,
		Arguments:        args,
	}
	return hosted.ToolResult{
		Output: output,
		Status: hosted.StatusCompleted,
		Summary: hosted.SafeSummary{
			Arguments: hosted.RedactedValue{Value: summarizeJSON(call.Arguments)},
			Output: hosted.RedactedValue{Value: map[string]any{
				"server_label": server.label,
				"tool_name":    toolName,
				"has_text":     tool.TextResult != "",
				"has_struct":   tool.StructuredResult != nil,
			}},
		},
	}, nil
}

func DescriptorFromProtocolTool(tool protocol.Tool) Descriptor {
	descriptor := Descriptor{
		Type:         strings.TrimSpace(tool.Type),
		ServerLabel:  strings.TrimSpace(tool.ServerLabel),
		AllowedTools: ParseToolNames(tool.AllowedTools),
	}
	if tool.Extra != nil {
		descriptor.DeniedTools = ParseToolNames(firstPresent(tool.Extra, "denied_tools", "deny_tools"))
	}
	return descriptor
}

func ParseToolNames(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return oneToolName(v)
	case []string:
		return normalizeToolNames(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, ParseToolNames(item)...)
		}
		return normalizeToolNames(out)
	case map[string]any:
		if raw, ok := v["tools"]; ok {
			return ParseToolNames(raw)
		}
		if raw, ok := v["tool_names"]; ok {
			return ParseToolNames(raw)
		}
		if raw, ok := v["names"]; ok {
			return ParseToolNames(raw)
		}
		if raw, ok := v["name"]; ok {
			return ParseToolNames(raw)
		}
	}
	return nil
}

func parseCallInput(call hosted.ToolCall) (callInput, map[string]any, error) {
	input := callInput{}
	args := map[string]any{}
	raw := strings.TrimSpace(string(call.Arguments))
	if raw == "" {
		return input, args, nil
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return callInput{}, nil, fmt.Errorf("decode mcp hosted tool arguments: %w", err)
	}
	if len(input.Arguments) > 0 {
		if err := json.Unmarshal(input.Arguments, &args); err != nil {
			return callInput{}, nil, fmt.Errorf("decode mcp nested arguments: %w", err)
		}
		return input, args, nil
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return callInput{}, nil, fmt.Errorf("decode mcp argument object: %w", err)
	}
	delete(args, "server_id")
	delete(args, "server_label")
	delete(args, "tool")
	delete(args, "name")
	return input, args, nil
}

func (e *Executor) resolveServer(key string) (serverRuntime, error) {
	key = strings.TrimSpace(key)
	if key != "" {
		server, ok := e.servers[key]
		if !ok {
			return serverRuntime{}, ErrUnknownServer
		}
		return server, nil
	}
	if e.defaultServerKey != "" {
		if server, ok := e.servers[e.defaultServerKey]; ok {
			return server, nil
		}
	}
	return serverRuntime{}, ErrUnknownServer
}

func summarizeJSON(raw json.RawMessage) map[string]any {
	text := strings.TrimSpace(string(raw))
	summary := map[string]any{
		"bytes":  len(raw),
		"sha256": sha256Hex(raw),
	}
	if len(text) <= maxInlineSummaryBytes {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err == nil {
			summary["value"] = decoded
		}
	} else {
		summary["redacted"] = true
	}
	return summary
}

func stringSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		name := normalizeToolName(value)
		if name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

func normalizeToolNames(values []string) []string {
	set := stringSet(values)
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func oneToolName(value string) []string {
	name := normalizeToolName(value)
	if name == "" {
		return nil
	}
	return []string{name}
}

func normalizeToolName(value string) string {
	return strings.TrimSpace(value)
}

func contains(set map[string]struct{}, value string) bool {
	_, ok := set[normalizeToolName(value)]
	return ok
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPresent(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
