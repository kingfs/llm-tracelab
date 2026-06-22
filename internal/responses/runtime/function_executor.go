package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type FunctionToolExecutor interface {
	ExecuteFunctionTool(ctx context.Context, call FunctionToolCall) (FunctionToolResult, error)
}

type FunctionToolExecutorFunc func(ctx context.Context, call FunctionToolCall) (FunctionToolResult, error)

func (f FunctionToolExecutorFunc) ExecuteFunctionTool(ctx context.Context, call FunctionToolCall) (FunctionToolResult, error) {
	return f(ctx, call)
}

type FunctionToolCall struct {
	CallID    string
	Name      string
	Arguments string
}

type FunctionToolResult struct {
	Output any
}

type FunctionToolExecutorPolicy struct {
	Timeout         time.Duration
	MaxResultBytes  int
	RedactArguments bool
	RedactOutput    bool
}

type StaticFunctionToolExecutor struct {
	Output any
}

func (e StaticFunctionToolExecutor) ExecuteFunctionTool(ctx context.Context, call FunctionToolCall) (FunctionToolResult, error) {
	if err := ctx.Err(); err != nil {
		return FunctionToolResult{}, err
	}
	return FunctionToolResult(e), nil
}

type FunctionToolResultTooLargeError struct {
	Name           string
	MaxResultBytes int
	ResultBytes    int
}

func (e FunctionToolResultTooLargeError) Error() string {
	if e.Name == "" {
		return fmt.Sprintf("function tool result is too large: %d bytes exceeds max_result_bytes %d", e.ResultBytes, e.MaxResultBytes)
	}
	return fmt.Sprintf("function tool %q result is too large: %d bytes exceeds max_result_bytes %d", e.Name, e.ResultBytes, e.MaxResultBytes)
}

type configuredFunctionToolExecutor struct {
	executor FunctionToolExecutor
	policy   FunctionToolExecutorPolicy
}

func normalizeFunctionToolName(name string) string {
	return strings.TrimSpace(name)
}
