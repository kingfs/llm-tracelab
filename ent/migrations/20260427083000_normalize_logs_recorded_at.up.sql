-- normalize "logs.recorded_at" from legacy TEXT storage to ent-compatible datetime storage
DROP INDEX IF EXISTS `idx_logs_recorded_at`;
DROP INDEX IF EXISTS `idx_logs_model_recorded_at`;
DROP INDEX IF EXISTS `idx_logs_trace_id`;
DROP INDEX IF EXISTS `idx_logs_session_id_recorded_at`;
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
  `recorded_at` datetime NOT NULL,
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
  `is_stream` bool NOT NULL DEFAULT (false),
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
  `path`, CASE WHEN `trace_id` IS NULL OR TRIM(`trace_id`) = '' THEN lower(hex(randomblob(16))) ELSE `trace_id` END, `mod_time_ns`, `file_size`, `version`, `request_id`,
  CASE WHEN `recorded_at` IS NULL OR TRIM(CAST(`recorded_at` AS text)) = '' THEN '1970-01-01T00:00:00Z' ELSE `recorded_at` END,
  `model`, `provider`, `operation`, `endpoint`, `url`, `method`, `status_code`, `duration_ms`, `ttft_ms`,
  `client_ip`, `content_length`, `error_text`, `prompt_tokens`, `completion_tokens`, `total_tokens`,
  `cached_tokens`, `req_header_len`, `req_body_len`, `res_header_len`, `res_body_len`,
  CASE WHEN `is_stream` IN (1, '1', 'true', 'TRUE') THEN true ELSE false END,
  `session_id`, `session_source`, `window_id`, `client_request_id`, `selected_upstream_id`,
  `selected_upstream_base_url`, `selected_upstream_provider_preset`, `routing_policy`, `routing_score`,
  `routing_candidate_count`, `routing_failure_reason`
FROM `logs_old`;
DROP TABLE `logs_old`;
CREATE UNIQUE INDEX `logs_trace_id_key` ON `logs` (`trace_id`);
CREATE INDEX `tracelog_recorded_at` ON `logs` (`recorded_at`);
CREATE INDEX `tracelog_model_recorded_at` ON `logs` (`model`, `recorded_at`);
CREATE INDEX `tracelog_session_id_recorded_at` ON `logs` (`session_id`, `recorded_at`);
CREATE INDEX `tracelog_request_id` ON `logs` (`request_id`);
