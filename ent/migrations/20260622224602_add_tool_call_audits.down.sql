-- reverse: create index "upstreamexchange_status_code_started_at" to table: "upstream_exchanges"
DROP INDEX `upstreamexchange_status_code_started_at`;
-- reverse: create index "upstreamexchange_upstream_id_started_at" to table: "upstream_exchanges"
DROP INDEX `upstreamexchange_upstream_id_started_at`;
-- reverse: create index "upstreamexchange_trace_id" to table: "upstream_exchanges"
DROP INDEX `upstreamexchange_trace_id`;
-- reverse: create index "upstreamexchange_request_audit_id_started_at" to table: "upstream_exchanges"
DROP INDEX `upstreamexchange_request_audit_id_started_at`;
-- reverse: create index "upstreamexchange_response_id_started_at" to table: "upstream_exchanges"
DROP INDEX `upstreamexchange_response_id_started_at`;
-- reverse: set sequence for "upstream_exchanges" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "upstream_exchanges";
-- reverse: create "upstream_exchanges" table
DROP TABLE `upstream_exchanges`;
-- reverse: create index "traceobservation_status_updated_at" to table: "trace_observations"
DROP INDEX `traceobservation_status_updated_at`;
-- reverse: set sequence for "trace_observations" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "trace_observations";
-- reverse: create "trace_observations" table
DROP TABLE `trace_observations`;
-- reverse: create index "tracefinding_trace_id_severity_category" to table: "trace_findings"
DROP INDEX `tracefinding_trace_id_severity_category`;
-- reverse: create index "tracefinding_trace_id_finding_id" to table: "trace_findings"
DROP INDEX `tracefinding_trace_id_finding_id`;
-- reverse: set sequence for "trace_findings" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "trace_findings";
-- reverse: create "trace_findings" table
DROP TABLE `trace_findings`;
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
-- reverse: set sequence for "tool_call_audits" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "tool_call_audits";
-- reverse: create "tool_call_audits" table
DROP TABLE `tool_call_audits`;
-- reverse: create index "systemevent_trace_id_last_seen_at" to table: "system_events"
DROP INDEX `systemevent_trace_id_last_seen_at`;
-- reverse: create index "systemevent_source_category_last_seen_at" to table: "system_events"
DROP INDEX `systemevent_source_category_last_seen_at`;
-- reverse: create index "systemevent_status_last_seen_at" to table: "system_events"
DROP INDEX `systemevent_status_last_seen_at`;
-- reverse: create index "system_events_fingerprint_key" to table: "system_events"
DROP INDEX `system_events_fingerprint_key`;
-- reverse: set sequence for "system_events" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "system_events";
-- reverse: create "system_events" table
DROP TABLE `system_events`;
-- reverse: create index "semanticnode_trace_id_depth_node_index" to table: "semantic_nodes"
DROP INDEX `semanticnode_trace_id_depth_node_index`;
-- reverse: create index "semanticnode_trace_id_node_id" to table: "semantic_nodes"
DROP INDEX `semanticnode_trace_id_node_id`;
-- reverse: set sequence for "semantic_nodes" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "semantic_nodes";
-- reverse: create "semantic_nodes" table
DROP TABLE `semantic_nodes`;
-- reverse: create index "responseitem_response_id_kind" to table: "response_items"
DROP INDEX `responseitem_response_id_kind`;
-- reverse: create index "responseitem_conversation_id_created_at" to table: "response_items"
DROP INDEX `responseitem_conversation_id_created_at`;
-- reverse: set sequence for "response_items" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "response_items";
-- reverse: create "response_items" table
DROP TABLE `response_items`;
-- reverse: create index "response_status_created_at" to table: "responses"
DROP INDEX `response_status_created_at`;
-- reverse: create index "response_previous_response_id" to table: "responses"
DROP INDEX `response_previous_response_id`;
-- reverse: create index "response_conversation_id_created_at" to table: "responses"
DROP INDEX `response_conversation_id_created_at`;
-- reverse: set sequence for "responses" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "responses";
-- reverse: create "responses" table
DROP TABLE `responses`;
-- reverse: create index "requestaudit_status_created_at" to table: "request_audits"
DROP INDEX `requestaudit_status_created_at`;
-- reverse: create index "requestaudit_client_request_id" to table: "request_audits"
DROP INDEX `requestaudit_client_request_id`;
-- reverse: create index "requestaudit_conversation_id_created_at" to table: "request_audits"
DROP INDEX `requestaudit_conversation_id_created_at`;
-- reverse: create index "requestaudit_response_id_created_at" to table: "request_audits"
DROP INDEX `requestaudit_response_id_created_at`;
-- reverse: set sequence for "request_audits" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "request_audits";
-- reverse: create "request_audits" table
DROP TABLE `request_audits`;
-- reverse: create index "parserversion_parser_version" to table: "parser_versions"
DROP INDEX `parserversion_parser_version`;
-- reverse: set sequence for "parser_versions" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "parser_versions";
-- reverse: create "parser_versions" table
DROP TABLE `parser_versions`;
-- reverse: create index "parsejob_status_updated_at" to table: "parse_jobs"
DROP INDEX `parsejob_status_updated_at`;
-- reverse: set sequence for "parse_jobs" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "parse_jobs";
-- reverse: create "parse_jobs" table
DROP TABLE `parse_jobs`;
-- reverse: create index "executionevent_phase_status_occurred_at" to table: "execution_events"
DROP INDEX `executionevent_phase_status_occurred_at`;
-- reverse: create index "executionevent_event_type_occurred_at" to table: "execution_events"
DROP INDEX `executionevent_event_type_occurred_at`;
-- reverse: create index "executionevent_conversation_id_occurred_at" to table: "execution_events"
DROP INDEX `executionevent_conversation_id_occurred_at`;
-- reverse: create index "executionevent_response_id_occurred_at" to table: "execution_events"
DROP INDEX `executionevent_response_id_occurred_at`;
-- reverse: create index "executionevent_request_audit_id_occurred_at" to table: "execution_events"
DROP INDEX `executionevent_request_audit_id_occurred_at`;
-- reverse: set sequence for "execution_events" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "execution_events";
-- reverse: create "execution_events" table
DROP TABLE `execution_events`;
-- reverse: create index "analysisrun_trace_id_kind_created_at" to table: "analysis_runs"
DROP INDEX `analysisrun_trace_id_kind_created_at`;
-- reverse: create index "analysisrun_session_id_kind_created_at" to table: "analysis_runs"
DROP INDEX `analysisrun_session_id_kind_created_at`;
-- reverse: set sequence for "analysis_runs" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "analysis_runs";
-- reverse: create "analysis_runs" table
DROP TABLE `analysis_runs`;
-- reverse: create index "analysisjob_target_type_target_id_created_at" to table: "analysis_jobs"
DROP INDEX `analysisjob_target_type_target_id_created_at`;
-- reverse: create index "analysisjob_status_updated_at" to table: "analysis_jobs"
DROP INDEX `analysisjob_status_updated_at`;
-- reverse: set sequence for "analysis_jobs" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "analysis_jobs";
-- reverse: create "analysis_jobs" table
DROP TABLE `analysis_jobs`;
-- reverse: create index "channelconfig_provider_preset" to table: "channel_configs"
DROP INDEX `channelconfig_provider_preset`;
-- reverse: create index "channelconfig_enabled_priority" to table: "channel_configs"
DROP INDEX `channelconfig_enabled_priority`;
-- reverse: set sequence for "new_channel_configs" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "new_channel_configs";
-- reverse: create "new_channel_configs" table
DROP TABLE `new_channel_configs`;
-- reverse: create index "tracelog_request_id" to table: "logs"
DROP INDEX `tracelog_request_id`;
-- reverse: create index "tracelog_session_id_recorded_at" to table: "logs"
DROP INDEX `tracelog_session_id_recorded_at`;
-- reverse: create index "tracelog_model_recorded_at" to table: "logs"
DROP INDEX `tracelog_model_recorded_at`;
-- reverse: create index "tracelog_recorded_at" to table: "logs"
DROP INDEX `tracelog_recorded_at`;
-- reverse: create index "logs_trace_id_key" to table: "logs"
DROP INDEX `logs_trace_id_key`;
-- reverse: set sequence for "new_logs" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "new_logs";
-- reverse: create "new_logs" table
DROP TABLE `new_logs`;
