package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultExternalCommandCaptureBytes = 64 << 10

type ExternalCommandFunctionToolExecutor struct {
	Command            string
	Args               []string
	Env                map[string]string
	EnvAllowlist       []string
	WorkingDir         string
	RequireAbsPath     bool
	AllowedCommandDirs []string
	RejectRoot         bool
	Timeout            time.Duration
	MaxStdoutBytes     int
	MaxStderrBytes     int
}

type externalCommandFunctionToolInput struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (e ExternalCommandFunctionToolExecutor) ExecuteFunctionTool(ctx context.Context, call FunctionToolCall) (FunctionToolResult, error) {
	if strings.TrimSpace(e.Command) == "" {
		return FunctionToolResult{}, fmt.Errorf("external command function executor command is required")
	}
	workingDir, err := e.validatedWorkingDir()
	if err != nil {
		return FunctionToolResult{}, err
	}
	if e.RequireAbsPath && !filepath.IsAbs(e.Command) {
		return FunctionToolResult{}, fmt.Errorf("external command function executor command must be absolute")
	}
	if err := e.validateSandboxConstraints(); err != nil {
		return FunctionToolResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return FunctionToolResult{}, err
	}
	execCtx := ctx
	cancel := func() {}
	if e.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, e.Timeout)
	}
	defer cancel()

	input, err := json.Marshal(externalCommandFunctionToolInput(call))
	if err != nil {
		return FunctionToolResult{}, fmt.Errorf("marshal external command function input: %w", err)
	}
	input = append(input, '\n')

	cmd := exec.CommandContext(execCtx, e.Command, e.Args...)
	cmd.Env = e.commandEnv()
	cmd.Dir = workingDir
	cmd.Stdin = bytes.NewReader(input)

	stdout := newLimitedCapture(e.stdoutLimit())
	stderr := newLimitedCapture(e.stderrLimit())
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	if execCtx.Err() != nil {
		return FunctionToolResult{}, execCtx.Err()
	}
	if err != nil {
		return FunctionToolResult{}, externalCommandError(err, stderr.String())
	}
	return FunctionToolResult{Output: stdout.String()}, nil
}

func (e ExternalCommandFunctionToolExecutor) validatedWorkingDir() (string, error) {
	workingDir := strings.TrimSpace(e.WorkingDir)
	if workingDir == "" {
		return "", nil
	}
	if !filepath.IsAbs(workingDir) {
		return "", fmt.Errorf("external command function executor working_dir must be absolute")
	}
	info, err := os.Stat(workingDir)
	if err != nil {
		return "", fmt.Errorf("external command function executor working_dir is not accessible: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("external command function executor working_dir must be a directory")
	}
	return workingDir, nil
}

func (e ExternalCommandFunctionToolExecutor) validateSandboxConstraints() error {
	if e.RejectRoot && os.Geteuid() == 0 {
		return fmt.Errorf("external command function executor refuses to run as root")
	}
	if len(e.AllowedCommandDirs) == 0 {
		return nil
	}
	if !filepath.IsAbs(e.Command) {
		return fmt.Errorf("external command function executor command must be absolute when allowed_command_dirs is configured")
	}
	resolvedCommand, err := filepath.EvalSymlinks(e.Command)
	if err != nil {
		return fmt.Errorf("external command function executor command is not accessible or cannot be resolved: %w", err)
	}
	resolvedDirs := make([]string, 0, len(e.AllowedCommandDirs))
	for _, dir := range e.AllowedCommandDirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		resolvedDir, err := validatedAllowedCommandDir(dir)
		if err != nil {
			return err
		}
		resolvedDirs = append(resolvedDirs, resolvedDir)
	}
	if len(resolvedDirs) == 0 {
		return nil
	}
	for _, resolvedDir := range resolvedDirs {
		if pathWithinDir(resolvedCommand, resolvedDir) {
			return nil
		}
	}
	return fmt.Errorf("external command function executor command is outside allowed_command_dirs")
}

func validatedAllowedCommandDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("external command function executor allowed_command_dirs entries must be absolute")
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("external command function executor allowed_command_dirs entry is not accessible or cannot be resolved: %w", err)
	}
	info, err := os.Stat(resolvedDir)
	if err != nil {
		return "", fmt.Errorf("external command function executor allowed_command_dirs entry is not accessible: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("external command function executor allowed_command_dirs entries must be directories")
	}
	return resolvedDir, nil
}

func pathWithinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (e ExternalCommandFunctionToolExecutor) stdoutLimit() int {
	if e.MaxStdoutBytes > 0 {
		return e.MaxStdoutBytes
	}
	return defaultExternalCommandCaptureBytes
}

func (e ExternalCommandFunctionToolExecutor) stderrLimit() int {
	if e.MaxStderrBytes > 0 {
		return e.MaxStderrBytes
	}
	return defaultExternalCommandCaptureBytes
}

func (e ExternalCommandFunctionToolExecutor) commandEnv() []string {
	env := make([]string, 0, len(e.Env)+len(e.EnvAllowlist))
	for _, key := range e.EnvAllowlist {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	for key, value := range e.Env {
		key = strings.TrimSpace(key)
		if key == "" || strings.Contains(key, "=") {
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}

func externalCommandError(err error, stderr string) error {
	summary := strings.TrimSpace(stderr)
	if summary == "" {
		return fmt.Errorf("external command function executor failed: %w", err)
	}
	return fmt.Errorf("external command function executor failed: %w: stderr: %s", err, summary)
}

type limitedCapture struct {
	buf       bytes.Buffer
	limit     int
	total     int
	truncated bool
}

func newLimitedCapture(limit int) *limitedCapture {
	if limit < 0 {
		limit = 0
	}
	return &limitedCapture{limit: limit}
}

func (c *limitedCapture) Write(p []byte) (int, error) {
	c.total += len(p)
	remaining := c.limit - c.buf.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = c.buf.Write(p)
		} else {
			_, _ = c.buf.Write(p[:remaining])
			c.truncated = true
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil
}

func (c *limitedCapture) String() string {
	out := c.buf.String()
	if c.truncated {
		out += fmt.Sprintf("\n<truncated: captured %d of %d bytes>", c.buf.Len(), c.total)
	}
	return out
}
