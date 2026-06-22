-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_logs" table
CREATE TABLE `new_logs` (`path` text NOT NULL, `trace_id` text NOT NULL, `mod_time_ns` integer NOT NULL, `file_size` integer NOT NULL, `version` text NOT NULL, `request_id` text NOT NULL DEFAULT (''), `recorded_at` datetime NOT NULL, `model` text NOT NULL DEFAULT (''), `provider` text NOT NULL DEFAULT (''), `operation` text NOT NULL DEFAULT (''), `endpoint` text NOT NULL DEFAULT (''), `url` text NOT NULL DEFAULT (''), `method` text NOT NULL DEFAULT (''), `status_code` integer NOT NULL DEFAULT (0), `duration_ms` integer NOT NULL DEFAULT (0), `ttft_ms` integer NOT NULL DEFAULT (0), `client_ip` text NOT NULL DEFAULT (''), `content_length` integer NOT NULL DEFAULT (0), `error_text` text NOT NULL DEFAULT (''), `prompt_tokens` integer NOT NULL DEFAULT (0), `completion_tokens` integer NOT NULL DEFAULT (0), `total_tokens` integer NOT NULL DEFAULT (0), `cached_tokens` integer NOT NULL DEFAULT (0), `req_header_len` integer NOT NULL DEFAULT (0), `req_body_len` integer NOT NULL DEFAULT (0), `res_header_len` integer NOT NULL DEFAULT (0), `res_body_len` integer NOT NULL DEFAULT (0), `is_stream` bool NOT NULL DEFAULT (false), `session_id` text NOT NULL DEFAULT (''), `session_source` text NOT NULL DEFAULT (''), `window_id` text NOT NULL DEFAULT (''), `client_request_id` text NOT NULL DEFAULT (''), `selected_upstream_id` text NOT NULL DEFAULT (''), `selected_upstream_base_url` text NOT NULL DEFAULT (''), `selected_upstream_provider_preset` text NOT NULL DEFAULT (''), `routing_policy` text NOT NULL DEFAULT (''), `routing_score` real NOT NULL DEFAULT (0), `routing_candidate_count` integer NOT NULL DEFAULT (0), `routing_failure_reason` text NOT NULL DEFAULT (''), PRIMARY KEY (`path`));
-- set sequence for "new_logs" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("new_logs", 30064771072);
-- copy rows from old table "logs" to new temporary table "new_logs"
INSERT INTO `new_logs` (`path`, `trace_id`, `mod_time_ns`, `file_size`, `version`, `request_id`, `recorded_at`, `model`, `provider`, `operation`, `endpoint`, `url`, `method`, `status_code`, `duration_ms`, `ttft_ms`, `client_ip`, `content_length`, `error_text`, `prompt_tokens`, `completion_tokens`, `total_tokens`, `cached_tokens`, `req_header_len`, `req_body_len`, `res_header_len`, `res_body_len`, `is_stream`, `session_id`, `session_source`, `window_id`, `client_request_id`, `selected_upstream_id`, `selected_upstream_base_url`, `selected_upstream_provider_preset`, `routing_policy`, `routing_score`, `routing_candidate_count`, `routing_failure_reason`) SELECT `path`, `trace_id`, `mod_time_ns`, `file_size`, `version`, `request_id`, `recorded_at`, `model`, `provider`, `operation`, `endpoint`, `url`, `method`, `status_code`, `duration_ms`, `ttft_ms`, `client_ip`, `content_length`, `error_text`, `prompt_tokens`, `completion_tokens`, `total_tokens`, `cached_tokens`, `req_header_len`, `req_body_len`, `res_header_len`, `res_body_len`, `is_stream`, `session_id`, `session_source`, `window_id`, `client_request_id`, `selected_upstream_id`, `selected_upstream_base_url`, `selected_upstream_provider_preset`, `routing_policy`, `routing_score`, `routing_candidate_count`, `routing_failure_reason` FROM `logs`;
-- drop "logs" table after copying rows
DROP TABLE `logs`;
-- rename temporary table "new_logs" to "logs"
ALTER TABLE `new_logs` RENAME TO `logs`;
-- create index "logs_trace_id_key" to table: "logs"
CREATE UNIQUE INDEX `logs_trace_id_key` ON `logs` (`trace_id`);
-- create index "tracelog_recorded_at" to table: "logs"
CREATE INDEX `tracelog_recorded_at` ON `logs` (`recorded_at`);
-- create index "tracelog_model_recorded_at" to table: "logs"
CREATE INDEX `tracelog_model_recorded_at` ON `logs` (`model`, `recorded_at`);
-- create index "tracelog_session_id_recorded_at" to table: "logs"
CREATE INDEX `tracelog_session_id_recorded_at` ON `logs` (`session_id`, `recorded_at`);
-- create index "tracelog_request_id" to table: "logs"
CREATE INDEX `tracelog_request_id` ON `logs` (`request_id`);
-- create "new_channel_configs" table
CREATE TABLE `new_channel_configs` (`id` text NOT NULL, `name` text NOT NULL, `description` text NOT NULL DEFAULT (''), `source` text NOT NULL DEFAULT ('manual'), `base_url` text NOT NULL, `provider_preset` text NOT NULL DEFAULT (''), `api_type` text NOT NULL DEFAULT (''), `mode` text NOT NULL DEFAULT (''), `capabilities_json` text NOT NULL DEFAULT ('{}'), `protocol_family` text NOT NULL DEFAULT (''), `routing_profile` text NOT NULL DEFAULT (''), `api_version` text NOT NULL DEFAULT (''), `deployment` text NOT NULL DEFAULT (''), `project` text NOT NULL DEFAULT (''), `location` text NOT NULL DEFAULT (''), `model_resource` text NOT NULL DEFAULT (''), `api_key_ciphertext` blob NULL, `api_key_hint` text NOT NULL DEFAULT (''), `headers_json` text NOT NULL DEFAULT ('{}'), `enabled` bool NOT NULL DEFAULT (true), `priority` integer NOT NULL DEFAULT (0), `weight` real NOT NULL DEFAULT (1), `capacity_hint` real NOT NULL DEFAULT (1), `model_discovery` text NOT NULL DEFAULT ('list_models'), `allow_unknown_models` bool NOT NULL DEFAULT (false), `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `last_probe_at` datetime NULL, `last_probe_status` text NOT NULL DEFAULT (''), `last_probe_error` text NOT NULL DEFAULT (''), PRIMARY KEY (`id`));
-- set sequence for "new_channel_configs" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("new_channel_configs", 42949672960);
-- copy rows from old table "channel_configs" to new temporary table "new_channel_configs"
INSERT INTO `new_channel_configs` (`id`, `name`, `description`, `base_url`, `provider_preset`, `api_type`, `mode`, `capabilities_json`, `protocol_family`, `routing_profile`, `api_version`, `deployment`, `project`, `location`, `model_resource`, `api_key_ciphertext`, `api_key_hint`, `headers_json`, `enabled`, `priority`, `weight`, `capacity_hint`, `model_discovery`, `allow_unknown_models`, `created_at`, `updated_at`, `last_probe_at`, `last_probe_status`, `last_probe_error`) SELECT `id`, `name`, `description`, `base_url`, `provider_preset`, `api_type`, `mode`, `capabilities_json`, `protocol_family`, `routing_profile`, `api_version`, `deployment`, `project`, `location`, `model_resource`, `api_key_ciphertext`, `api_key_hint`, `headers_json`, `enabled`, `priority`, `weight`, `capacity_hint`, `model_discovery`, `allow_unknown_models`, `created_at`, `updated_at`, `last_probe_at`, `last_probe_status`, `last_probe_error` FROM `channel_configs`;
-- drop "channel_configs" table after copying rows
DROP TABLE `channel_configs`;
-- rename temporary table "new_channel_configs" to "channel_configs"
ALTER TABLE `new_channel_configs` RENAME TO `channel_configs`;
-- create index "channelconfig_enabled_priority" to table: "channel_configs"
CREATE INDEX `channelconfig_enabled_priority` ON `channel_configs` (`enabled`, `priority`);
-- create index "channelconfig_provider_preset" to table: "channel_configs"
CREATE INDEX `channelconfig_provider_preset` ON `channel_configs` (`provider_preset`);
-- create "analysis_jobs" table
CREATE TABLE `analysis_jobs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `job_type` text NOT NULL, `target_type` text NOT NULL, `target_id` text NOT NULL, `status` text NOT NULL, `steps_json` text NOT NULL DEFAULT ('[]'), `request_json` text NOT NULL DEFAULT ('{}'), `result_json` text NOT NULL DEFAULT ('{}'), `last_error` text NOT NULL DEFAULT (''), `attempts` integer NOT NULL DEFAULT (0), `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `started_at` datetime NULL, `finished_at` datetime NULL);
-- set sequence for "analysis_jobs" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("analysis_jobs", 81604378624);
-- create index "analysisjob_status_updated_at" to table: "analysis_jobs"
CREATE INDEX `analysisjob_status_updated_at` ON `analysis_jobs` (`status`, `updated_at`);
-- create index "analysisjob_target_type_target_id_created_at" to table: "analysis_jobs"
CREATE INDEX `analysisjob_target_type_target_id_created_at` ON `analysis_jobs` (`target_type`, `target_id`, `created_at`);
-- create "analysis_runs" table
CREATE TABLE `analysis_runs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `trace_id` text NOT NULL DEFAULT (''), `session_id` text NOT NULL DEFAULT (''), `kind` text NOT NULL, `analyzer` text NOT NULL, `analyzer_version` text NOT NULL, `model` text NOT NULL DEFAULT (''), `input_ref` text NOT NULL DEFAULT (''), `output_json` text NOT NULL DEFAULT ('{}'), `status` text NOT NULL, `created_at` datetime NOT NULL);
-- set sequence for "analysis_runs" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("analysis_runs", 85899345920);
-- create index "analysisrun_session_id_kind_created_at" to table: "analysis_runs"
CREATE INDEX `analysisrun_session_id_kind_created_at` ON `analysis_runs` (`session_id`, `kind`, `created_at`);
-- create index "analysisrun_trace_id_kind_created_at" to table: "analysis_runs"
CREATE INDEX `analysisrun_trace_id_kind_created_at` ON `analysis_runs` (`trace_id`, `kind`, `created_at`);
-- create "execution_events" table
CREATE TABLE `execution_events` (`id` text NOT NULL, `response_id` text NULL, `request_audit_id` text NULL, `conversation_id` text NULL, `event_type` text NOT NULL, `phase` text NOT NULL DEFAULT (''), `status` text NOT NULL DEFAULT (''), `message` text NULL, `details_json` json NULL, `occurred_at` datetime NOT NULL, PRIMARY KEY (`id`));
-- set sequence for "execution_events" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("execution_events", 68719476736);
-- create index "executionevent_request_audit_id_occurred_at" to table: "execution_events"
CREATE INDEX `executionevent_request_audit_id_occurred_at` ON `execution_events` (`request_audit_id`, `occurred_at`);
-- create index "executionevent_response_id_occurred_at" to table: "execution_events"
CREATE INDEX `executionevent_response_id_occurred_at` ON `execution_events` (`response_id`, `occurred_at`);
-- create index "executionevent_conversation_id_occurred_at" to table: "execution_events"
CREATE INDEX `executionevent_conversation_id_occurred_at` ON `execution_events` (`conversation_id`, `occurred_at`);
-- create index "executionevent_event_type_occurred_at" to table: "execution_events"
CREATE INDEX `executionevent_event_type_occurred_at` ON `execution_events` (`event_type`, `occurred_at`);
-- create index "executionevent_phase_status_occurred_at" to table: "execution_events"
CREATE INDEX `executionevent_phase_status_occurred_at` ON `execution_events` (`phase`, `status`, `occurred_at`);
-- create "parse_jobs" table
CREATE TABLE `parse_jobs` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `trace_id` text NOT NULL, `status` text NOT NULL, `attempts` integer NOT NULL DEFAULT (0), `last_error` text NOT NULL DEFAULT (''), `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL);
-- set sequence for "parse_jobs" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("parse_jobs", 90194313216);
-- create index "parsejob_status_updated_at" to table: "parse_jobs"
CREATE INDEX `parsejob_status_updated_at` ON `parse_jobs` (`status`, `updated_at`);
-- create "parser_versions" table
CREATE TABLE `parser_versions` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `parser` text NOT NULL, `version` text NOT NULL, `description` text NOT NULL DEFAULT (''), `created_at` datetime NOT NULL);
-- set sequence for "parser_versions" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("parser_versions", 94489280512);
-- create index "parserversion_parser_version" to table: "parser_versions"
CREATE UNIQUE INDEX `parserversion_parser_version` ON `parser_versions` (`parser`, `version`);
-- create "request_audits" table
CREATE TABLE `request_audits` (`id` text NOT NULL, `response_id` text NULL, `conversation_id` text NULL, `method` text NOT NULL, `path` text NOT NULL, `client_request_id` text NULL, `header_json` json NULL, `body_preview` text NULL, `body_sha256` text NULL, `redaction_json` json NULL, `status` text NOT NULL DEFAULT (''), `error_text` text NULL, `created_at` datetime NOT NULL, PRIMARY KEY (`id`));
-- set sequence for "request_audits" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("request_audits", 73014444032);
-- create index "requestaudit_response_id_created_at" to table: "request_audits"
CREATE INDEX `requestaudit_response_id_created_at` ON `request_audits` (`response_id`, `created_at`);
-- create index "requestaudit_conversation_id_created_at" to table: "request_audits"
CREATE INDEX `requestaudit_conversation_id_created_at` ON `request_audits` (`conversation_id`, `created_at`);
-- create index "requestaudit_client_request_id" to table: "request_audits"
CREATE INDEX `requestaudit_client_request_id` ON `request_audits` (`client_request_id`);
-- create index "requestaudit_status_created_at" to table: "request_audits"
CREATE INDEX `requestaudit_status_created_at` ON `request_audits` (`status`, `created_at`);
-- create "responses" table
CREATE TABLE `responses` (`id` text NOT NULL, `conversation_id` text NOT NULL DEFAULT (''), `previous_response_id` text NOT NULL DEFAULT (''), `status` text NOT NULL DEFAULT ('queued'), `model` text NOT NULL, `history_item_ids` json NULL, `output_item_ids` json NULL, `effective_tools` json NULL, `metadata` json NULL, `usage` json NULL, `error` json NULL, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, PRIMARY KEY (`id`));
-- set sequence for "responses" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("responses", 60129542144);
-- create index "response_conversation_id_created_at" to table: "responses"
CREATE INDEX `response_conversation_id_created_at` ON `responses` (`conversation_id`, `created_at`);
-- create index "response_previous_response_id" to table: "responses"
CREATE INDEX `response_previous_response_id` ON `responses` (`previous_response_id`);
-- create index "response_status_created_at" to table: "responses"
CREATE INDEX `response_status_created_at` ON `responses` (`status`, `created_at`);
-- create "response_items" table
CREATE TABLE `response_items` (`id` text NOT NULL, `kind` text NOT NULL, `response_id` text NOT NULL DEFAULT (''), `conversation_id` text NOT NULL DEFAULT (''), `payload` json NOT NULL, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, PRIMARY KEY (`id`));
-- set sequence for "response_items" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("response_items", 64424509440);
-- create index "responseitem_conversation_id_created_at" to table: "response_items"
CREATE INDEX `responseitem_conversation_id_created_at` ON `response_items` (`conversation_id`, `created_at`);
-- create index "responseitem_response_id_kind" to table: "response_items"
CREATE INDEX `responseitem_response_id_kind` ON `response_items` (`response_id`, `kind`);
-- create "semantic_nodes" table
CREATE TABLE `semantic_nodes` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `trace_id` text NOT NULL, `node_id` text NOT NULL, `parent_node_id` text NOT NULL DEFAULT (''), `provider_type` text NOT NULL DEFAULT (''), `normalized_type` text NOT NULL DEFAULT (''), `role` text NOT NULL DEFAULT (''), `path` text NOT NULL DEFAULT (''), `node_index` integer NOT NULL DEFAULT (0), `depth` integer NOT NULL DEFAULT (0), `text_preview` text NOT NULL DEFAULT (''), `json` text NOT NULL DEFAULT (''), `raw` text NOT NULL DEFAULT (''), `raw_ref` text NOT NULL DEFAULT (''), `created_at` datetime NOT NULL);
-- set sequence for "semantic_nodes" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("semantic_nodes", 98784247808);
-- create index "semanticnode_trace_id_node_id" to table: "semantic_nodes"
CREATE UNIQUE INDEX `semanticnode_trace_id_node_id` ON `semantic_nodes` (`trace_id`, `node_id`);
-- create index "semanticnode_trace_id_depth_node_index" to table: "semantic_nodes"
CREATE INDEX `semanticnode_trace_id_depth_node_index` ON `semantic_nodes` (`trace_id`, `depth`, `node_index`);
-- create "system_events" table
CREATE TABLE `system_events` (`id` text NOT NULL, `fingerprint` text NOT NULL, `source` text NOT NULL, `category` text NOT NULL, `severity` text NOT NULL, `status` text NOT NULL, `title` text NOT NULL DEFAULT (''), `message` text NOT NULL DEFAULT (''), `details_json` text NOT NULL DEFAULT ('{}'), `trace_id` text NOT NULL DEFAULT (''), `session_id` text NOT NULL DEFAULT (''), `job_id` text NOT NULL DEFAULT (''), `upstream_id` text NOT NULL DEFAULT (''), `model` text NOT NULL DEFAULT (''), `occurrence_count` integer NOT NULL DEFAULT (1), `first_seen_at` datetime NOT NULL, `last_seen_at` datetime NOT NULL, `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, `read_at` datetime NULL, `resolved_at` datetime NULL, PRIMARY KEY (`id`));
-- set sequence for "system_events" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("system_events", 103079215104);
-- create index "system_events_fingerprint_key" to table: "system_events"
CREATE UNIQUE INDEX `system_events_fingerprint_key` ON `system_events` (`fingerprint`);
-- create index "systemevent_status_last_seen_at" to table: "system_events"
CREATE INDEX `systemevent_status_last_seen_at` ON `system_events` (`status`, `last_seen_at`);
-- create index "systemevent_source_category_last_seen_at" to table: "system_events"
CREATE INDEX `systemevent_source_category_last_seen_at` ON `system_events` (`source`, `category`, `last_seen_at`);
-- create index "systemevent_trace_id_last_seen_at" to table: "system_events"
CREATE INDEX `systemevent_trace_id_last_seen_at` ON `system_events` (`trace_id`, `last_seen_at`);
-- create "tool_call_audits" table
CREATE TABLE `tool_call_audits` (`id` text NOT NULL, `response_id` text NULL, `request_audit_id` text NULL, `conversation_id` text NULL, `call_id` text NOT NULL, `tool_type` text NOT NULL, `tool_name` text NULL, `executor` text NULL, `status` text NOT NULL DEFAULT (''), `phase` text NOT NULL DEFAULT ('tool_call'), `input_json` json NULL, `output_json` json NULL, `error_text` text NULL, `metadata_json` json NULL, `started_at` datetime NULL, `completed_at` datetime NULL, `created_at` datetime NOT NULL, PRIMARY KEY (`id`));
-- set sequence for "tool_call_audits" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("tool_call_audits", 115964116992);
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
-- create "trace_findings" table
CREATE TABLE `trace_findings` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `trace_id` text NOT NULL, `finding_id` text NOT NULL, `category` text NOT NULL, `severity` text NOT NULL, `confidence` real NOT NULL DEFAULT (0), `title` text NOT NULL DEFAULT (''), `description` text NOT NULL DEFAULT (''), `evidence_path` text NOT NULL DEFAULT (''), `evidence_excerpt` text NOT NULL DEFAULT (''), `node_id` text NOT NULL DEFAULT (''), `detector` text NOT NULL, `detector_version` text NOT NULL, `created_at` datetime NOT NULL);
-- set sequence for "trace_findings" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("trace_findings", 107374182400);
-- create index "tracefinding_trace_id_finding_id" to table: "trace_findings"
CREATE UNIQUE INDEX `tracefinding_trace_id_finding_id` ON `trace_findings` (`trace_id`, `finding_id`);
-- create index "tracefinding_trace_id_severity_category" to table: "trace_findings"
CREATE INDEX `tracefinding_trace_id_severity_category` ON `trace_findings` (`trace_id`, `severity`, `category`);
-- create "trace_observations" table
CREATE TABLE `trace_observations` (`trace_id` text NOT NULL, `parser` text NOT NULL, `parser_version` text NOT NULL, `status` text NOT NULL, `provider` text NOT NULL DEFAULT (''), `operation` text NOT NULL DEFAULT (''), `model` text NOT NULL DEFAULT (''), `summary_json` text NOT NULL DEFAULT ('{}'), `warnings_json` text NOT NULL DEFAULT ('[]'), `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, PRIMARY KEY (`trace_id`));
-- set sequence for "trace_observations" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("trace_observations", 111669149696);
-- create index "traceobservation_status_updated_at" to table: "trace_observations"
CREATE INDEX `traceobservation_status_updated_at` ON `trace_observations` (`status`, `updated_at`);
-- create "upstream_exchanges" table
CREATE TABLE `upstream_exchanges` (`id` text NOT NULL, `response_id` text NULL, `request_audit_id` text NULL, `trace_id` text NULL, `cassette_path` text NULL, `upstream_id` text NULL, `route_target` text NULL, `model` text NULL, `endpoint` text NULL, `status_code` integer NULL, `started_at` datetime NULL, `completed_at` datetime NULL, `error_text` text NULL, PRIMARY KEY (`id`));
-- set sequence for "upstream_exchanges" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("upstream_exchanges", 77309411328);
-- create index "upstreamexchange_response_id_started_at" to table: "upstream_exchanges"
CREATE INDEX `upstreamexchange_response_id_started_at` ON `upstream_exchanges` (`response_id`, `started_at`);
-- create index "upstreamexchange_request_audit_id_started_at" to table: "upstream_exchanges"
CREATE INDEX `upstreamexchange_request_audit_id_started_at` ON `upstream_exchanges` (`request_audit_id`, `started_at`);
-- create index "upstreamexchange_trace_id" to table: "upstream_exchanges"
CREATE INDEX `upstreamexchange_trace_id` ON `upstream_exchanges` (`trace_id`);
-- create index "upstreamexchange_upstream_id_started_at" to table: "upstream_exchanges"
CREATE INDEX `upstreamexchange_upstream_id_started_at` ON `upstream_exchanges` (`upstream_id`, `started_at`);
-- create index "upstreamexchange_status_code_started_at" to table: "upstream_exchanges"
CREATE INDEX `upstreamexchange_status_code_started_at` ON `upstream_exchanges` (`status_code`, `started_at`);
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
