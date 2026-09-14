package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func (s *Store) SaveAppSettingJSON(ctx context.Context, key string, value any) error {
	if s == nil || s.db == nil {
		return errorsNewStoreClosed()
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("setting key is required")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal app setting %q: %w", key, err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO app_settings (setting_key, value_json, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(setting_key) DO UPDATE SET
			value_json = excluded.value_json,
			updated_at = excluded.updated_at`, key, string(payload)); err != nil {
		return fmt.Errorf("save app setting %q: %w", key, err)
	}
	return nil
}

func (s *Store) LoadAppSettingJSON(ctx context.Context, key string, out any) (bool, error) {
	if s == nil || s.db == nil {
		return false, errorsNewStoreClosed()
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return false, fmt.Errorf("setting key is required")
	}
	if out == nil {
		return false, fmt.Errorf("setting output is required")
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_settings WHERE setting_key = ?`, key).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("load app setting %q: %w", key, err)
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return false, fmt.Errorf("decode app setting %q: %w", key, err)
	}
	return true, nil
}

// DeleteAppSetting removes one app setting and reports whether a row existed.
func (s *Store) DeleteAppSetting(ctx context.Context, key string) (bool, error) {
	if s == nil || s.db == nil {
		return false, errorsNewStoreClosed()
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return false, fmt.Errorf("setting key is required")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM app_settings WHERE setting_key = ?`, key)
	if err != nil {
		return false, fmt.Errorf("delete app setting %q: %w", key, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete app setting %q: %w", key, err)
	}
	return affected > 0, nil
}
