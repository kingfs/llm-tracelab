package runtime

import (
	"context"
	"strings"
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

func normalizeFunctionToolName(name string) string {
	return strings.TrimSpace(name)
}
