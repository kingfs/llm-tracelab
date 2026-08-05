package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExternalCommandFunctionToolExecutorSuccessSendsCallOnStdin(t *testing.T) {
	executor := externalCommandTestExecutor("echo-input")

	result, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{
		CallID:    "call_lookup",
		Name:      "lookup",
		Arguments: `{"q":"codex"}`,
	})
	if err != nil {
		t.Fatalf("ExecuteFunctionTool() error = %v", err)
	}

	var input externalCommandFunctionToolInput
	if err := json.Unmarshal([]byte(result.Output.(string)), &input); err != nil {
		t.Fatalf("output is not stdin JSON: %v; output=%q", err, result.Output)
	}
	if input.CallID != "call_lookup" || input.Name != "lookup" || input.Arguments != `{"q":"codex"}` {
		t.Fatalf("stdin JSON = %+v, want call id/name/arguments", input)
	}
}

func TestExternalCommandFunctionToolExecutorDoesNotInheritEnvironmentByDefault(t *testing.T) {
	t.Setenv("LLM_TRACELAB_EXTERNAL_EXECUTOR_SECRET", "do-not-leak")
	executor := externalCommandTestExecutor("env")

	result, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if err != nil {
		t.Fatalf("ExecuteFunctionTool() error = %v", err)
	}
	if got := result.Output.(string); strings.Contains(got, "do-not-leak") || strings.Contains(got, "LLM_TRACELAB_EXTERNAL_EXECUTOR_SECRET") {
		t.Fatalf("executor inherited parent environment: %q", got)
	}
}

func TestExternalCommandFunctionToolExecutorSupportsStaticEnvAndAllowlist(t *testing.T) {
	t.Setenv("LLM_TRACELAB_ALLOWED_EXECUTOR_ENV", "allowed")
	executor := externalCommandTestExecutor("env")
	executor.Env["STATIC_VALUE"] = "static"
	executor.EnvAllowlist = []string{"LLM_TRACELAB_ALLOWED_EXECUTOR_ENV"}

	result, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if err != nil {
		t.Fatalf("ExecuteFunctionTool() error = %v", err)
	}
	output := result.Output.(string)
	if !strings.Contains(output, "STATIC_VALUE=static") || !strings.Contains(output, "LLM_TRACELAB_ALLOWED_EXECUTOR_ENV=allowed") {
		t.Fatalf("executor env = %q, want static and allowlisted values", output)
	}
}

func TestExternalCommandFunctionToolExecutorTimeout(t *testing.T) {
	executor := externalCommandTestExecutor("sleep")
	executor.Timeout = 20 * time.Millisecond

	_, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ExecuteFunctionTool() error = %v, want context deadline exceeded", err)
	}
}

func TestExternalCommandFunctionToolExecutorContextCancel(t *testing.T) {
	executor := externalCommandTestExecutor("sleep")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := executor.ExecuteFunctionTool(ctx, FunctionToolCall{Name: "lookup"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteFunctionTool() error = %v, want context canceled", err)
	}
}

func TestExternalCommandFunctionToolExecutorNonZeroExitIncludesLimitedStderrSummary(t *testing.T) {
	executor := externalCommandTestExecutor("fail")
	executor.MaxStderrBytes = 8

	_, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if err == nil {
		t.Fatalf("ExecuteFunctionTool() error = nil, want failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "exit status 7") || !strings.Contains(msg, "stderr: stderr-l") || !strings.Contains(msg, "truncated") {
		t.Fatalf("ExecuteFunctionTool() error = %q, want exit status and limited stderr summary", msg)
	}
	if strings.Contains(msg, "stderr-line-with-extra-detail") {
		t.Fatalf("ExecuteFunctionTool() error leaked full stderr: %q", msg)
	}
}

func TestExternalCommandFunctionToolExecutorLimitsStdoutCapture(t *testing.T) {
	executor := externalCommandTestExecutor("large-output")
	executor.MaxStdoutBytes = 5

	result, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if err != nil {
		t.Fatalf("ExecuteFunctionTool() error = %v", err)
	}
	if got := result.Output.(string); !strings.HasPrefix(got, "abcde") || !strings.Contains(got, "truncated") {
		t.Fatalf("output = %q, want limited capture marker", got)
	}
}

func TestExternalCommandFunctionToolExecutorDoesNotUseShellForArgs(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "injected")
	executor := externalCommandTestExecutor("args", ";", "touch", marker)

	result, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if err != nil {
		t.Fatalf("ExecuteFunctionTool() error = %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shell injection marker exists or stat failed unexpectedly: %v", err)
	}
	if got := result.Output.(string); !strings.Contains(got, marker) {
		t.Fatalf("output args = %q, want literal marker arg", got)
	}
}

func TestExternalCommandFunctionToolExecutorUsesConfiguredWorkingDir(t *testing.T) {
	workingDir := t.TempDir()
	executor := externalCommandTestExecutor("cwd")
	executor.WorkingDir = workingDir

	result, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if err != nil {
		t.Fatalf("ExecuteFunctionTool() error = %v", err)
	}
	gotDir, err := filepath.EvalSymlinks(strings.TrimSpace(result.Output.(string)))
	if err != nil {
		t.Fatalf("EvalSymlinks(output) error = %v", err)
	}
	wantDir, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(workingDir) error = %v", err)
	}
	if gotDir != wantDir {
		t.Fatalf("working directory = %q, want %q", gotDir, wantDir)
	}
}

func TestExternalCommandFunctionToolExecutorRejectsRelativeWorkingDir(t *testing.T) {
	executor := externalCommandTestExecutor("cwd")
	executor.WorkingDir = "relative-dir"

	_, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if err == nil || !strings.Contains(err.Error(), "working_dir must be absolute") {
		t.Fatalf("ExecuteFunctionTool() error = %v, want absolute working_dir error", err)
	}
}

func TestExternalCommandFunctionToolExecutorCanRequireAbsoluteCommand(t *testing.T) {
	executor := externalCommandTestExecutor("echo-input")
	executor.Command = filepath.Base(executor.Command)
	executor.RequireAbsPath = true

	_, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if err == nil || !strings.Contains(err.Error(), "command must be absolute") {
		t.Fatalf("ExecuteFunctionTool() error = %v, want absolute command error", err)
	}
}

func TestExternalCommandFunctionToolExecutorAllowedCommandDirsAllowsCommandInsideDir(t *testing.T) {
	executor := externalCommandTestExecutor("echo-input")
	resolvedCommand, err := filepath.EvalSymlinks(executor.Command)
	if err != nil {
		t.Fatalf("EvalSymlinks(command) error = %v", err)
	}
	executor.AllowedCommandDirs = []string{filepath.Dir(resolvedCommand)}

	result, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if err != nil {
		t.Fatalf("ExecuteFunctionTool() error = %v", err)
	}
	if got := result.Output.(string); !strings.Contains(got, `"name":"lookup"`) {
		t.Fatalf("output = %q, want helper output", got)
	}
}

func TestExternalCommandFunctionToolExecutorAllowedCommandDirsRejectsCommandOutsideDir(t *testing.T) {
	executor := externalCommandTestExecutor("echo-input")
	executor.AllowedCommandDirs = []string{t.TempDir()}

	_, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if err == nil || !strings.Contains(err.Error(), "command is outside allowed_command_dirs") {
		t.Fatalf("ExecuteFunctionTool() error = %v, want outside allowed dirs error", err)
	}
}

func TestExternalCommandFunctionToolExecutorAllowedCommandDirsRejectsRelativeDir(t *testing.T) {
	executor := externalCommandTestExecutor("echo-input")
	executor.AllowedCommandDirs = []string{"relative-bin"}

	_, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if err == nil || !strings.Contains(err.Error(), "allowed_command_dirs entries must be absolute") {
		t.Fatalf("ExecuteFunctionTool() error = %v, want absolute allowed dir error", err)
	}
}

func TestExternalCommandFunctionToolExecutorRejectRoot(t *testing.T) {
	executor := externalCommandTestExecutor("echo-input")
	executor.RejectRoot = true

	result, err := executor.ExecuteFunctionTool(context.Background(), FunctionToolCall{Name: "lookup"})
	if os.Geteuid() == 0 {
		if err == nil || !strings.Contains(err.Error(), "refuses to run as root") {
			t.Fatalf("ExecuteFunctionTool() error = %v, want root rejection", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("ExecuteFunctionTool() error = %v", err)
	}
	if got := result.Output.(string); !strings.Contains(got, `"name":"lookup"`) {
		t.Fatalf("output = %q, want helper output", got)
	}
}

func externalCommandTestExecutor(mode string, extraArgs ...string) ExternalCommandFunctionToolExecutor {
	args := append([]string{"-test.run=TestExternalCommandExecutorHelper", "--", mode}, extraArgs...)
	return ExternalCommandFunctionToolExecutor{
		Command: os.Args[0],
		Args:    args,
		Env:     map[string]string{"LLM_TRACELAB_EXTERNAL_EXECUTOR_HELPER": "1"},
	}
}

func TestExternalCommandExecutorHelper(t *testing.T) {
	if os.Getenv("LLM_TRACELAB_EXTERNAL_EXECUTOR_HELPER") != "1" {
		return
	}
	args := os.Args
	modeIndex := -1
	for i, arg := range args {
		if arg == "--" && i+1 < len(args) {
			modeIndex = i + 1
			break
		}
	}
	if modeIndex < 0 {
		fmt.Fprintln(os.Stderr, "missing mode")
		os.Exit(2)
	}
	switch args[modeIndex] {
	case "echo-input":
		input, _ := io.ReadAll(os.Stdin)
		fmt.Print(string(input))
	case "env":
		for _, env := range os.Environ() {
			fmt.Println(env)
		}
	case "sleep":
		time.Sleep(time.Second)
	case "fail":
		fmt.Fprint(os.Stderr, "stderr-line-with-extra-detail")
		os.Exit(7)
	case "large-output":
		fmt.Print("abcdefghijklmnopqrstuvwxyz")
	case "args":
		fmt.Println(strings.Join(args[modeIndex+1:], "|"))
	case "cwd":
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "getwd: %v", err)
			os.Exit(3)
		}
		fmt.Println(wd)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q", args[modeIndex])
		os.Exit(2)
	}
	os.Exit(0)
}
