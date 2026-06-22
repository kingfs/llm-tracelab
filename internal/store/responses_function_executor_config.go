package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kingfs/llm-tracelab/internal/config"
)

const responsesFunctionExecutorConfigSnapshotKey = "responses.function_executors.config_snapshot"

type responsesFunctionExecutorConfigSnapshot struct {
	Enabled        bool                                       `json:"enabled"`
	Timeout        string                                     `json:"timeout"`
	MaxResultBytes int                                        `json:"max_result_bytes"`
	Redaction      responsesFunctionExecutorRedactionSnapshot `json:"redaction"`
	Executors      []responsesFunctionExecutorBindingSnapshot `json:"executors"`
}

type responsesFunctionExecutorRedactionSnapshot struct {
	Arguments bool `json:"arguments"`
	Output    bool `json:"output"`
}

type responsesFunctionExecutorBindingSnapshot struct {
	Name    string                                   `json:"name"`
	Type    string                                   `json:"type"`
	Enabled *bool                                    `json:"enabled,omitempty"`
	Process responsesFunctionExecutorProcessSnapshot `json:"process,omitempty"`
}

type responsesFunctionExecutorProcessSnapshot struct {
	WorkingDir             string `json:"working_dir,omitempty"`
	RequireAbsoluteCommand bool   `json:"require_absolute_command,omitempty"`
}

// SaveResponsesFunctionExecutorConfigSnapshot stores only the non-sensitive
// Responses function executor configuration fields needed by Monitor.
func (s *Store) SaveResponsesFunctionExecutorConfigSnapshot(ctx context.Context, cfg config.ResponsesFunctionExecutorConfig) error {
	if s == nil || s.db == nil {
		return errorsNewStoreClosed()
	}
	payload, err := json.Marshal(newResponsesFunctionExecutorConfigSnapshot(cfg))
	if err != nil {
		return fmt.Errorf("marshal responses function executor config snapshot: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO app_settings (setting_key, value_json, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(setting_key) DO UPDATE SET
			value_json = excluded.value_json,
			updated_at = excluded.updated_at`, responsesFunctionExecutorConfigSnapshotKey, string(payload)); err != nil {
		return fmt.Errorf("save responses function executor config snapshot: %w", err)
	}
	return nil
}

// LoadResponsesFunctionExecutorConfigSnapshot returns the persisted safe
// snapshot. The bool is false when no snapshot has been saved.
func (s *Store) LoadResponsesFunctionExecutorConfigSnapshot(ctx context.Context) (config.ResponsesFunctionExecutorConfig, bool, error) {
	if s == nil || s.db == nil {
		return config.ResponsesFunctionExecutorConfig{}, false, errorsNewStoreClosed()
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE setting_key = ?`, responsesFunctionExecutorConfigSnapshotKey).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return config.ResponsesFunctionExecutorConfig{}, false, nil
		}
		return config.ResponsesFunctionExecutorConfig{}, false, fmt.Errorf("load responses function executor config snapshot: %w", err)
	}
	var snapshot responsesFunctionExecutorConfigSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return config.ResponsesFunctionExecutorConfig{}, false, fmt.Errorf("decode responses function executor config snapshot: %w", err)
	}
	cfg, err := snapshot.toConfig()
	if err != nil {
		return config.ResponsesFunctionExecutorConfig{}, false, err
	}
	return cfg, true, nil
}

func newResponsesFunctionExecutorConfigSnapshot(cfg config.ResponsesFunctionExecutorConfig) responsesFunctionExecutorConfigSnapshot {
	snapshot := responsesFunctionExecutorConfigSnapshot{
		Enabled:        cfg.Enabled,
		Timeout:        cfg.Timeout.String(),
		MaxResultBytes: cfg.MaxResultBytes,
		Redaction: responsesFunctionExecutorRedactionSnapshot{
			Arguments: cfg.Redaction.Arguments,
			Output:    cfg.Redaction.Output,
		},
		Executors: make([]responsesFunctionExecutorBindingSnapshot, 0, len(cfg.Executors)),
	}
	for _, binding := range cfg.Executors {
		snapshot.Executors = append(snapshot.Executors, responsesFunctionExecutorBindingSnapshot{
			Name:    strings.TrimSpace(binding.Name),
			Type:    strings.ToLower(strings.TrimSpace(binding.Type)),
			Enabled: cloneBoolPtr(binding.Enabled),
			Process: responsesFunctionExecutorProcessSnapshot{
				WorkingDir:             strings.TrimSpace(binding.Process.WorkingDir),
				RequireAbsoluteCommand: binding.Process.RequireAbsoluteCommand,
			},
		})
	}
	return snapshot
}

func (snapshot responsesFunctionExecutorConfigSnapshot) toConfig() (config.ResponsesFunctionExecutorConfig, error) {
	var timeout time.Duration
	if strings.TrimSpace(snapshot.Timeout) != "" {
		parsed, err := time.ParseDuration(snapshot.Timeout)
		if err != nil {
			return config.ResponsesFunctionExecutorConfig{}, fmt.Errorf("decode responses function executor config snapshot timeout: %w", err)
		}
		timeout = parsed
	}
	cfg := config.ResponsesFunctionExecutorConfig{
		Enabled:        snapshot.Enabled,
		Timeout:        timeout,
		MaxResultBytes: snapshot.MaxResultBytes,
		Redaction: config.ResponsesFunctionRedactionConfig{
			Arguments: snapshot.Redaction.Arguments,
			Output:    snapshot.Redaction.Output,
		},
		Executors: make([]config.ResponsesFunctionExecutorBinding, 0, len(snapshot.Executors)),
	}
	for _, binding := range snapshot.Executors {
		cfg.Executors = append(cfg.Executors, config.ResponsesFunctionExecutorBinding{
			Name:    strings.TrimSpace(binding.Name),
			Type:    strings.ToLower(strings.TrimSpace(binding.Type)),
			Enabled: cloneBoolPtr(binding.Enabled),
			Process: config.ResponsesFunctionExecutorProcessConfig{
				WorkingDir:             strings.TrimSpace(binding.Process.WorkingDir),
				RequireAbsoluteCommand: binding.Process.RequireAbsoluteCommand,
			},
		})
	}
	return cfg, nil
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func errorsNewStoreClosed() error {
	return fmt.Errorf("store is closed")
}
