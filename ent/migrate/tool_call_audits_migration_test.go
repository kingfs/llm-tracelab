package entmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteToolCallAuditsMigrationContainsTableAndIndexes(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "migrations", "*_add_tool_call_audits.up.sql"))
	if err != nil {
		t.Fatalf("glob migration: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("tool_call_audits migrations = %v, want exactly one", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := string(data)
	for _, want := range []string{
		"CREATE TABLE `tool_call_audits`",
		"`call_id` text NOT NULL",
		"`tool_type` text NOT NULL",
		"CREATE INDEX `toolcallaudit_request_audit_id_created_at`",
		"CREATE INDEX `toolcallaudit_response_id_created_at`",
		"CREATE INDEX `toolcallaudit_conversation_id_created_at`",
		"CREATE INDEX `toolcallaudit_call_id`",
		"CREATE INDEX `toolcallaudit_tool_name_status_created_at`",
		"CREATE INDEX `toolcallaudit_status_created_at`",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("migration %s missing %q", matches[0], want)
		}
	}
}
