-- create "tool_call_audits" table
CREATE TABLE `tool_call_audits` (
	`id` text NOT NULL,
	`response_id` text NULL,
	`request_audit_id` text NULL,
	`conversation_id` text NULL,
	`call_id` text NOT NULL,
	`tool_type` text NOT NULL,
	`tool_name` text NULL,
	`executor` text NULL,
	`status` text NOT NULL DEFAULT (''),
	`phase` text NOT NULL DEFAULT ('tool_call'),
	`input_json` json NULL,
	`output_json` json NULL,
	`error_text` text NULL,
	`metadata_json` json NULL,
	`started_at` datetime NULL,
	`completed_at` datetime NULL,
	`created_at` datetime NOT NULL,
	PRIMARY KEY (`id`)
);
-- create index "toolcallaudit_request_audit_id_created_at" to table: "tool_call_audits"
CREATE INDEX `toolcallaudit_request_audit_id_created_at` ON `tool_call_audits` (`request_audit_id`, `created_at`);
-- create index "toolcallaudit_response_id_created_at" to table: "tool_call_audits"
CREATE INDEX `toolcallaudit_response_id_created_at` ON `tool_call_audits` (`response_id`, `created_at`);
-- create index "toolcallaudit_conversation_id_created_at" to table: "tool_call_audits"
CREATE INDEX `toolcallaudit_conversation_id_created_at` ON `tool_call_audits` (`conversation_id`, `created_at`);
-- create index "toolcallaudit_call_id" to table: "tool_call_audits"
CREATE INDEX `toolcallaudit_call_id` ON `tool_call_audits` (`call_id`);
-- create index "toolcallaudit_tool_name_status_created_at" to table: "tool_call_audits"
CREATE INDEX `toolcallaudit_tool_name_status_created_at` ON `tool_call_audits` (`tool_name`, `status`, `created_at`);
-- create index "toolcallaudit_status_created_at" to table: "tool_call_audits"
CREATE INDEX `toolcallaudit_status_created_at` ON `tool_call_audits` (`status`, `created_at`);
