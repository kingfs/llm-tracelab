CREATE TABLE IF NOT EXISTS "tool_call_audits" (
	"id" character varying NOT NULL,
	"response_id" character varying NULL,
	"request_audit_id" character varying NULL,
	"conversation_id" character varying NULL,
	"call_id" character varying NOT NULL,
	"tool_type" character varying NOT NULL,
	"tool_name" character varying NULL,
	"executor" character varying NULL,
	"status" character varying NOT NULL DEFAULT '',
	"phase" character varying NOT NULL DEFAULT 'tool_call',
	"input_json" jsonb NULL,
	"output_json" jsonb NULL,
	"error_text" character varying NULL,
	"metadata_json" jsonb NULL,
	"started_at" timestamptz NULL,
	"completed_at" timestamptz NULL,
	"created_at" timestamptz NOT NULL,
	PRIMARY KEY ("id")
);

CREATE INDEX IF NOT EXISTS "toolcallaudit_request_audit_id_created_at" ON "tool_call_audits" ("request_audit_id", "created_at");
CREATE INDEX IF NOT EXISTS "toolcallaudit_response_id_created_at" ON "tool_call_audits" ("response_id", "created_at");
CREATE INDEX IF NOT EXISTS "toolcallaudit_conversation_id_created_at" ON "tool_call_audits" ("conversation_id", "created_at");
CREATE INDEX IF NOT EXISTS "toolcallaudit_call_id" ON "tool_call_audits" ("call_id");
CREATE INDEX IF NOT EXISTS "toolcallaudit_tool_name_status_created_at" ON "tool_call_audits" ("tool_name", "status", "created_at");
CREATE INDEX IF NOT EXISTS "toolcallaudit_status_created_at" ON "tool_call_audits" ("status", "created_at");
