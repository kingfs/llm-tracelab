-- reverse: create index "toolcallaudit_status_created_at" to table: "tool_call_audits"
DROP INDEX `toolcallaudit_status_created_at`;
-- reverse: create index "toolcallaudit_tool_name_status_created_at" to table: "tool_call_audits"
DROP INDEX `toolcallaudit_tool_name_status_created_at`;
-- reverse: create index "toolcallaudit_call_id" to table: "tool_call_audits"
DROP INDEX `toolcallaudit_call_id`;
-- reverse: create index "toolcallaudit_conversation_id_created_at" to table: "tool_call_audits"
DROP INDEX `toolcallaudit_conversation_id_created_at`;
-- reverse: create index "toolcallaudit_response_id_created_at" to table: "tool_call_audits"
DROP INDEX `toolcallaudit_response_id_created_at`;
-- reverse: create index "toolcallaudit_request_audit_id_created_at" to table: "tool_call_audits"
DROP INDEX `toolcallaudit_request_audit_id_created_at`;
-- reverse: create "tool_call_audits" table
DROP TABLE `tool_call_audits`;
