package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
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

type FunctionToolExecutorRegistration struct {
	Executor FunctionToolExecutor
	Policy   FunctionToolExecutorPolicy
}

type FunctionToolExecutorRegistry struct {
	mu        sync.RWMutex
	executors map[string]configuredFunctionToolExecutor
}

func NewFunctionToolExecutorRegistry(registrations map[string]FunctionToolExecutorRegistration) *FunctionToolExecutorRegistry {
	registry := &FunctionToolExecutorRegistry{}
	registry.Replace(registrations)
	return registry
}

func (r *FunctionToolExecutorRegistry) Set(name string, executor FunctionToolExecutor, policy FunctionToolExecutorPolicy) {
	if r == nil {
		return
	}
	name = normalizeFunctionToolName(name)
	if name == "" || executor == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.executors == nil {
		r.executors = map[string]configuredFunctionToolExecutor{}
	}
	r.executors[name] = configuredFunctionToolExecutor{
		executor: executor,
		policy:   policy,
	}
}

func (r *FunctionToolExecutorRegistry) Replace(registrations map[string]FunctionToolExecutorRegistration) {
	if r == nil {
		return
	}
	next := make(map[string]configuredFunctionToolExecutor, len(registrations))
	for name, registration := range registrations {
		name = normalizeFunctionToolName(name)
		if name == "" || registration.Executor == nil {
			continue
		}
		next[name] = configuredFunctionToolExecutor{
			executor: registration.Executor,
			policy:   registration.Policy,
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executors = next
}

func (r *FunctionToolExecutorRegistry) Snapshot() map[string]FunctionToolExecutorRegistration {
	snapshot := r.configuredSnapshot()
	out := make(map[string]FunctionToolExecutorRegistration, len(snapshot))
	for name, configured := range snapshot {
		out[name] = FunctionToolExecutorRegistration{
			Executor: configured.executor,
			Policy:   configured.policy,
		}
	}
	return out
}

func (r *FunctionToolExecutorRegistry) configuredSnapshot() map[string]configuredFunctionToolExecutor {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]configuredFunctionToolExecutor, len(r.executors))
	for name, configured := range r.executors {
		out[name] = configured
	}
	return out
}

func normalizeFunctionToolName(name string) string {
	return strings.TrimSpace(name)
}
