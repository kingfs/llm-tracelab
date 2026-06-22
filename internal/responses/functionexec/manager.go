package functionexec

import (
	"fmt"
	"sync"

	"github.com/kingfs/llm-tracelab/internal/config"
	responsesruntime "github.com/kingfs/llm-tracelab/internal/responses/runtime"
)

type Manager struct {
	mu       sync.RWMutex
	cfg      config.ResponsesFunctionExecutorConfig
	registry *responsesruntime.FunctionToolExecutorRegistry
}

func NewManager(cfg config.ResponsesFunctionExecutorConfig) (*Manager, error) {
	normalized := NormalizeConfig(cfg)
	registry, err := NewRegistry(normalized)
	if err != nil {
		return nil, err
	}
	return &Manager{
		cfg:      cloneConfig(normalized),
		registry: registry,
	}, nil
}

func (m *Manager) Config() config.ResponsesFunctionExecutorConfig {
	if m == nil {
		return NormalizeConfig(config.ResponsesFunctionExecutorConfig{})
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneConfig(m.cfg)
}

func (m *Manager) Registry() *responsesruntime.FunctionToolExecutorRegistry {
	if m == nil {
		return responsesruntime.NewFunctionToolExecutorRegistry(nil)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.registry
}

func (m *Manager) Apply(cfg config.ResponsesFunctionExecutorConfig) error {
	if m == nil {
		return nil
	}
	normalized := NormalizeConfig(cfg)
	registrations, err := Registrations(normalized)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.registry == nil {
		m.registry = responsesruntime.NewFunctionToolExecutorRegistry(nil)
	}
	m.registry.Replace(registrations)
	m.cfg = cloneConfig(normalized)
	return nil
}

func NormalizeConfig(cfg config.ResponsesFunctionExecutorConfig) config.ResponsesFunctionExecutorConfig {
	return (config.Config{
		ResponsesServer: config.ResponsesServerConfig{
			FunctionExecutors: cfg,
		},
	}).ResponsesFunctionExecutorsConfig()
}

func NewRegistry(cfg config.ResponsesFunctionExecutorConfig) (*responsesruntime.FunctionToolExecutorRegistry, error) {
	registrations, err := Registrations(cfg)
	if err != nil {
		return nil, err
	}
	return responsesruntime.NewFunctionToolExecutorRegistry(registrations), nil
}

func Registrations(cfg config.ResponsesFunctionExecutorConfig) (map[string]responsesruntime.FunctionToolExecutorRegistration, error) {
	executorConfig := NormalizeConfig(cfg)
	registrations := map[string]responsesruntime.FunctionToolExecutorRegistration{}
	if !executorConfig.Enabled {
		return registrations, nil
	}
	policy := responsesruntime.FunctionToolExecutorPolicy{
		Timeout:         executorConfig.Timeout,
		MaxResultBytes:  executorConfig.MaxResultBytes,
		RedactArguments: executorConfig.Redaction.Arguments,
		RedactOutput:    executorConfig.Redaction.Output,
	}
	for _, binding := range executorConfig.Executors {
		if !binding.Available {
			continue
		}
		if binding.Name == "" {
			return nil, fmt.Errorf("responses function executor name is required")
		}
		switch binding.Type {
		case config.ResponsesFunctionExecutorTypeStaticResponse:
			registrations[binding.Name] = responsesruntime.FunctionToolExecutorRegistration{
				Executor: responsesruntime.StaticFunctionToolExecutor{Output: binding.Output},
				Policy:   policy,
			}
		case config.ResponsesFunctionExecutorTypeExternalCommand:
			if binding.Command == "" {
				return nil, fmt.Errorf("responses external_command executor command is required for %q", binding.Name)
			}
			registrations[binding.Name] = responsesruntime.FunctionToolExecutorRegistration{
				Executor: responsesruntime.ExternalCommandFunctionToolExecutor{
					Command:        binding.Command,
					Args:           binding.Args,
					Env:            binding.Env,
					EnvAllowlist:   binding.EnvAllowlist,
					WorkingDir:     binding.Process.WorkingDir,
					RequireAbsPath: binding.Process.RequireAbsoluteCommand,
					Timeout:        binding.Timeout,
					MaxStdoutBytes: executorConfig.MaxResultBytes + 1,
					MaxStderrBytes: executorConfig.MaxResultBytes + 1,
				},
				Policy: policy,
			}
		default:
			return nil, fmt.Errorf("unsupported responses function executor type %q for %q", binding.Type, binding.Name)
		}
	}
	return registrations, nil
}

func ApplySafeOverlay(base, overlay config.ResponsesFunctionExecutorConfig) config.ResponsesFunctionExecutorConfig {
	out := cloneConfig(base)
	out.Enabled = overlay.Enabled
	if overlay.Timeout != 0 {
		out.Timeout = overlay.Timeout
	}
	if overlay.MaxResultBytes != 0 {
		out.MaxResultBytes = overlay.MaxResultBytes
	}
	out.Redaction = overlay.Redaction
	if len(overlay.Executors) == 0 {
		return out
	}
	byName := make(map[string]config.ResponsesFunctionExecutorBinding, len(base.Executors))
	for _, binding := range base.Executors {
		byName[binding.Name] = binding
	}
	out.Executors = make([]config.ResponsesFunctionExecutorBinding, 0, len(overlay.Executors))
	for _, patch := range overlay.Executors {
		binding := config.ResponsesFunctionExecutorBinding{}
		if baseBinding, ok := byName[patch.Name]; ok && (patch.Type == "" || patch.Type == baseBinding.Type) {
			binding = baseBinding
		}
		binding.Name = patch.Name
		if patch.Type != "" {
			binding.Type = patch.Type
		}
		binding.Enabled = cloneBoolPtr(patch.Enabled)
		binding.Process = patch.Process
		out.Executors = append(out.Executors, binding)
	}
	return out
}

func cloneConfig(cfg config.ResponsesFunctionExecutorConfig) config.ResponsesFunctionExecutorConfig {
	cfg.Warnings = append([]string{}, cfg.Warnings...)
	cfg.Executors = append([]config.ResponsesFunctionExecutorBinding(nil), cfg.Executors...)
	for i := range cfg.Executors {
		cfg.Executors[i].Enabled = cloneBoolPtr(cfg.Executors[i].Enabled)
		cfg.Executors[i].Args = append([]string{}, cfg.Executors[i].Args...)
		cfg.Executors[i].EnvAllowlist = append([]string{}, cfg.Executors[i].EnvAllowlist...)
		cfg.Executors[i].Warnings = append([]string{}, cfg.Executors[i].Warnings...)
		if cfg.Executors[i].Env != nil {
			env := make(map[string]string, len(cfg.Executors[i].Env))
			for key, value := range cfg.Executors[i].Env {
				env[key] = value
			}
			cfg.Executors[i].Env = env
		}
	}
	return cfg
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
