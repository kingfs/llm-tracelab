package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/responses/protocol"
	"github.com/kingfs/llm-tracelab/internal/responses/tools/hosted"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const maxInlineSummaryBytes = 256

var (
	ErrDisabled           = errors.New("mcp hosted tool executor is disabled")
	ErrMissingTool        = errors.New("mcp hosted tool call is missing tool name")
	ErrToolDenied         = errors.New("mcp hosted tool is denied")
	ErrToolNotAllowed     = errors.New("mcp hosted tool is not allowed")
	ErrUnknownServer      = errors.New("mcp hosted server is unknown")
	ErrUnknownTool        = errors.New("mcp hosted tool is unknown")
	ErrMissingBearerToken = errors.New("mcp hosted server bearer token is missing")
)

type Options struct {
	Enabled bool
	Servers []ServerDescriptor
}

type ServerDescriptor struct {
	ID             string
	Label          string
	Enabled        bool
	URL            string
	BearerToken    string
	BearerTokenEnv string
	Timeout        time.Duration
	MaxResultBytes int
	AllowedTools   []string
	DeniedTools    []string
	Tools          []ToolDescriptor
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
	id             string
	label          string
	enabled        bool
	url            string
	bearerToken    string
	bearerTokenEnv string
	timeout        time.Duration
	maxResultBytes int
	allow          map[string]struct{}
	deny           map[string]struct{}
	tools          map[string]ToolDescriptor
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
	IsError          bool           `json:"is_error,omitempty"`
	Arguments        map[string]any `json:"arguments,omitempty"`
}

type ResultTooLargeError struct {
	ToolName       string
	MaxResultBytes int
	ResultBytes    int
}

func (e ResultTooLargeError) Error() string {
	if e.ToolName == "" {
		return fmt.Sprintf("mcp hosted tool result is too large: %d bytes exceeds max_result_bytes %d", e.ResultBytes, e.MaxResultBytes)
	}
	return fmt.Sprintf("mcp hosted tool %q result is too large: %d bytes exceeds max_result_bytes %d", e.ToolName, e.ResultBytes, e.MaxResultBytes)
}

func NewExecutor(opts Options) *Executor {
	e := &Executor{
		enabled: opts.Enabled,
		servers: map[string]serverRuntime{},
	}
	for _, server := range opts.Servers {
		runtime := serverRuntime{
			id:             strings.TrimSpace(server.ID),
			label:          strings.TrimSpace(server.Label),
			enabled:        server.Enabled,
			url:            strings.TrimSpace(server.URL),
			bearerToken:    strings.TrimSpace(server.BearerToken),
			bearerTokenEnv: strings.TrimSpace(server.BearerTokenEnv),
			timeout:        server.Timeout,
			maxResultBytes: server.MaxResultBytes,
			allow:          stringSet(server.AllowedTools),
			deny:           stringSet(server.DeniedTools),
			tools:          map[string]ToolDescriptor{},
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

	if server.url != "" {
		if tool, ok := server.tools[toolName]; ok && !tool.Enabled {
			return hosted.ToolResult{}, ErrDisabled
		}
		return e.executeRemoteTool(ctx, server, toolName, args, call.Arguments)
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

func (e *Executor) executeRemoteTool(ctx context.Context, server serverRuntime, toolName string, args map[string]any, rawArgs json.RawMessage) (hosted.ToolResult, error) {
	if server.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, server.timeout)
		defer cancel()
	}
	token, err := bearerToken(server)
	if err != nil {
		return hosted.ToolResult{}, err
	}
	rpcResult, responseBytes, err := callRemoteTool(ctx, server, token, toolName, args)
	if err != nil {
		return hosted.ToolResult{}, sanitizeBearerError(err, token)
	}
	output := outputFromMCPResult(server, toolName, args, rpcResult)
	if server.maxResultBytes > 0 {
		encoded, err := json.Marshal(output)
		if err != nil {
			return hosted.ToolResult{}, err
		}
		if len(encoded) > server.maxResultBytes {
			return hosted.ToolResult{}, ResultTooLargeError{
				ToolName:       toolName,
				MaxResultBytes: server.maxResultBytes,
				ResultBytes:    len(encoded),
			}
		}
	}
	return hosted.ToolResult{
		Output: output,
		Status: hosted.StatusCompleted,
		Summary: hosted.SafeSummary{
			Arguments: hosted.RedactedValue{Value: summarizeJSON(rawArgs)},
			Output: hosted.RedactedValue{Value: map[string]any{
				"server_label":   server.label,
				"tool_name":      toolName,
				"has_text":       output.Text != "",
				"has_struct":     output.StructuredResult != nil,
				"is_error":       output.IsError,
				"response_bytes": responseBytes,
			}},
		},
	}, nil
}

func bearerToken(server serverRuntime) (string, error) {
	if server.bearerToken != "" {
		return server.bearerToken, nil
	}
	if server.bearerTokenEnv == "" {
		return "", nil
	}
	token := strings.TrimSpace(os.Getenv(server.bearerTokenEnv))
	if token == "" {
		return "", fmt.Errorf("%w: %s", ErrMissingBearerToken, server.bearerTokenEnv)
	}
	return token, nil
}

type jsonRPCRequest struct {
	JSONRPC string                `json:"jsonrpc"`
	ID      int                   `json:"id"`
	Method  string                `json:"method"`
	Params  mcpsdk.CallToolParams `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func callRemoteTool(ctx context.Context, server serverRuntime, token string, toolName string, args map[string]any) (*mcpsdk.CallToolResult, int, error) {
	body, err := json.Marshal(jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: mcpsdk.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	})
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("create mcp streamable http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("call mcp streamable http server: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("mcp streamable http server returned status %d", resp.StatusCode)
	}
	raw, err := readLimited(resp.Body, server.maxResultBytes)
	if err != nil {
		return nil, 0, err
	}
	message := extractJSONRPCMessage(raw)
	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(message, &rpcResp); err != nil {
		return nil, len(raw), fmt.Errorf("decode mcp json-rpc response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, len(raw), fmt.Errorf("mcp json-rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	var result mcpsdk.CallToolResult
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return nil, len(raw), fmt.Errorf("decode mcp tools/call result: %w", err)
	}
	return &result, len(raw), nil
}

func readLimited(r io.Reader, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return io.ReadAll(r)
	}
	raw, err := io.ReadAll(io.LimitReader(r, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBytes {
		return nil, ResultTooLargeError{MaxResultBytes: maxBytes, ResultBytes: len(raw)}
	}
	return raw, nil
}

func extractJSONRPCMessage(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if !bytes.HasPrefix(trimmed, []byte("event:")) && !bytes.HasPrefix(trimmed, []byte("data:")) {
		return trimmed
	}
	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if bytes.HasPrefix(line, []byte("data:")) {
			return bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		}
	}
	return trimmed
}

func outputFromMCPResult(server serverRuntime, toolName string, args map[string]any, result *mcpsdk.CallToolResult) Output {
	output := Output{
		ServerID:         server.id,
		ServerLabel:      server.label,
		ToolName:         toolName,
		StructuredResult: result.StructuredContent,
		IsError:          result.IsError,
		Arguments:        args,
	}
	var texts []string
	var otherContent []any
	for _, content := range result.Content {
		switch c := content.(type) {
		case *mcpsdk.TextContent:
			texts = append(texts, c.Text)
			if output.StructuredResult == nil {
				var decoded any
				if err := json.Unmarshal([]byte(c.Text), &decoded); err == nil {
					output.StructuredResult = decoded
				}
			}
		default:
			if encoded, err := json.Marshal(c); err == nil {
				var decoded any
				if err := json.Unmarshal(encoded, &decoded); err == nil {
					otherContent = append(otherContent, decoded)
				}
			}
		}
	}
	output.Text = strings.Join(texts, "\n")
	if output.StructuredResult == nil && len(otherContent) > 0 {
		output.StructuredResult = map[string]any{"content": otherContent}
	}
	return output
}

func sanitizeBearerError(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	text := strings.ReplaceAll(err.Error(), token, "[redacted]")
	return errors.New(text)
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
