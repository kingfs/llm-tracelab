-- reverse "logs.recorded_at" back to the legacy TEXT shape
DROP INDEX IF EXISTS `logs_trace_id_key`;
DROP INDEX IF EXISTS `tracelog_recorded_at`;
DROP INDEX IF EXISTS `tracelog_model_recorded_at`;
DROP INDEX IF EXISTS `tracelog_session_id_recorded_at`;
DROP INDEX IF EXISTS `tracelog_request_id`;
ALTER TABLE `logs` RENAME TO `logs_old`;
CREATE TABLE `logs` (
  `path` text NOT NULL,
  `trace_id` text NOT NULL DEFAULT (''),
  `mod_time_ns` integer NOT NULL,
  `file_size` integer NOT NULL,
  `version` text NOT NULL,
  `request_id` text NOT NULL DEFAULT (''),
  `recorded_at` text NOT NULL,
  `model` text NOT NULL DEFAULT (''),
  `provider` text NOT NULL DEFAULT (''),
  `operation` text NOT NULL DEFAULT (''),
  `endpoint` text NOT NULL DEFAULT (''),
  `url` text NOT NULL DEFAULT (''),
  `method` text NOT NULL DEFAULT (''),
  `status_code` integer NOT NULL DEFAULT (0),
  `duration_ms` integer NOT NULL DEFAULT (0),
  `ttft_ms` integer NOT NULL DEFAULT (0),
  `client_ip` text NOT NULL DEFAULT (''),
  `content_length` integer NOT NULL DEFAULT (0),
  `error_text` text NOT NULL DEFAULT (''),
  `prompt_tokens` integer NOT NULL DEFAULT (0),
  `completion_tokens` integer NOT NULL DEFAULT (0),
  `total_tokens` integer NOT NULL DEFAULT (0),
  `cached_tokens` integer NOT NULL DEFAULT (0),
  `req_header_len` integer NOT NULL DEFAULT (0),
  `req_body_len` integer NOT NULL DEFAULT (0),
  `res_header_len` integer NOT NULL DEFAULT (0),
  `res_body_len` integer NOT NULL DEFAULT (0),
  `is_stream` integer NOT NULL DEFAULT (0),
  `session_id` text NOT NULL DEFAULT (''),
  `session_source` text NOT NULL DEFAULT (''),
  `window_id` text NOT NULL DEFAULT (''),
  `client_request_id` text NOT NULL DEFAULT (''),
  `selected_upstream_id` text NOT NULL DEFAULT (''),
  `selected_upstream_base_url` text NOT NULL DEFAULT (''),
  `selected_upstream_provider_preset` text NOT NULL DEFAULT (''),
  `routing_policy` text NOT NULL DEFAULT (''),
  `routing_score` real NOT NULL DEFAULT (0),
  `routing_candidate_count` integer NOT NULL DEFAULT (0),
  `routing_failure_reason` text NOT NULL DEFAULT (''),
  PRIMARY KEY (`path`)
);
INSERT INTO `logs` (
  `path`, `trace_id`, `mod_time_ns`, `file_size`, `version`, `request_id`, `recorded_at`, `model`,
  `provider`, `operation`, `endpoint`, `url`, `method`, `status_code`, `duration_ms`, `ttft_ms`,
  `client_ip`, `content_length`, `error_text`, `prompt_tokens`, `completion_tokens`, `total_tokens`,
  `cached_tokens`, `req_header_len`, `req_body_len`, `res_header_len`, `res_body_len`, `is_stream`,
  `session_id`, `session_source`, `window_id`, `client_request_id`, `selected_upstream_id`,
  `selected_upstream_base_url`, `selected_upstream_provider_preset`, `routing_policy`, `routing_score`,
  `routing_candidate_count`, `routing_failure_reason`
)
SELECT
  `path`, `trace_id`, `mod_time_ns`, `file_size`, `version`, `request_id`, `recorded_at`, `model`,
  `provider`, `operation`, `endpoint`, `url`, `method`, `status_code`, `duration_ms`, `ttft_ms`,
  `client_ip`, `content_length`, `error_text`, `prompt_tokens`, `completion_tokens`, `total_tokens`,
  `cached_tokens`, `req_header_len`, `req_body_len`, `res_header_len`, `res_body_len`,
  CASE WHEN `is_stream` THEN 1 ELSE 0 END,
  `session_id`, `session_source`, `window_id`, `client_request_id`, `selected_upstream_id`,
  `selected_upstream_base_url`, `selected_upstream_provider_preset`, `routing_policy`, `routing_score`,
  `routing_candidate_count`, `routing_failure_reason`
FROM `logs_old`;
DROP TABLE `logs_old`;
CREATE UNIQUE INDEX `idx_logs_trace_id` ON `logs` (`trace_id`) WHERE `trace_id` <> '';
CREATE INDEX `idx_logs_recorded_at` ON `logs` (`recorded_at` DESC);
CREATE INDEX `idx_logs_model_recorded_at` ON `logs` (`model`, `recorded_at` DESC);
CREATE INDEX `idx_logs_session_id_recorded_at` ON `logs` (`session_id`, `recorded_at` DESC) WHERE `session_id` <> '';
