-- create "datasets" table
CREATE TABLE `datasets` (`id` text NOT NULL, `name` text NOT NULL, `description` text NOT NULL DEFAULT (''), `created_at` datetime NOT NULL, `updated_at` datetime NOT NULL, PRIMARY KEY (`id`));
-- set sequence for "datasets" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("datasets", 8589934592);
-- create index "dataset_updated_at" to table: "datasets"
CREATE INDEX `dataset_updated_at` ON `datasets` (`updated_at`);
-- create "dataset_examples" table
CREATE TABLE `dataset_examples` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `dataset_id` text NOT NULL, `trace_id` text NOT NULL, `position` integer NOT NULL DEFAULT (0), `added_at` datetime NOT NULL, `source_type` text NOT NULL DEFAULT (''), `source_id` text NOT NULL DEFAULT (''), `note` text NOT NULL DEFAULT (''));
-- set sequence for "dataset_examples" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("dataset_examples", 12884901888);
-- create index "datasetexample_dataset_id_trace_id" to table: "dataset_examples"
CREATE UNIQUE INDEX `datasetexample_dataset_id_trace_id` ON `dataset_examples` (`dataset_id`, `trace_id`);
-- create index "datasetexample_dataset_id_position" to table: "dataset_examples"
CREATE INDEX `datasetexample_dataset_id_position` ON `dataset_examples` (`dataset_id`, `position`);
-- create "eval_runs" table
CREATE TABLE `eval_runs` (`id` text NOT NULL, `dataset_id` text NOT NULL DEFAULT (''), `source_type` text NOT NULL DEFAULT (''), `source_id` text NOT NULL DEFAULT (''), `evaluator_set` text NOT NULL, `created_at` datetime NOT NULL, `completed_at` datetime NOT NULL, `trace_count` integer NOT NULL DEFAULT (0), `score_count` integer NOT NULL DEFAULT (0), `pass_count` integer NOT NULL DEFAULT (0), `fail_count` integer NOT NULL DEFAULT (0), PRIMARY KEY (`id`));
-- set sequence for "eval_runs" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("eval_runs", 17179869184);
-- create index "evalrun_created_at" to table: "eval_runs"
CREATE INDEX `evalrun_created_at` ON `eval_runs` (`created_at`);
-- create "experiment_runs" table
CREATE TABLE `experiment_runs` (`id` text NOT NULL, `name` text NOT NULL DEFAULT (''), `description` text NOT NULL DEFAULT (''), `baseline_eval_run_id` text NOT NULL, `candidate_eval_run_id` text NOT NULL, `created_at` datetime NOT NULL, `baseline_score_count` integer NOT NULL DEFAULT (0), `candidate_score_count` integer NOT NULL DEFAULT (0), `baseline_pass_rate` real NOT NULL DEFAULT (0), `candidate_pass_rate` real NOT NULL DEFAULT (0), `pass_rate_delta` real NOT NULL DEFAULT (0), `matched_score_count` integer NOT NULL DEFAULT (0), `improvement_count` integer NOT NULL DEFAULT (0), `regression_count` integer NOT NULL DEFAULT (0), PRIMARY KEY (`id`));
-- set sequence for "experiment_runs" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("experiment_runs", 21474836480);
-- create index "experimentrun_created_at_id" to table: "experiment_runs"
CREATE INDEX `experimentrun_created_at_id` ON `experiment_runs` (`created_at`, `id`);
-- create "scores" table
CREATE TABLE `scores` (`id` text NOT NULL, `trace_id` text NOT NULL, `session_id` text NOT NULL DEFAULT (''), `dataset_id` text NOT NULL DEFAULT (''), `eval_run_id` text NOT NULL DEFAULT (''), `evaluator_key` text NOT NULL, `value` real NOT NULL DEFAULT (0), `status` text NOT NULL DEFAULT (''), `label` text NOT NULL DEFAULT (''), `explanation` text NOT NULL DEFAULT (''), `created_at` datetime NOT NULL, PRIMARY KEY (`id`));
-- set sequence for "scores" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("scores", 25769803776);
-- create index "score_trace_id_created_at" to table: "scores"
CREATE INDEX `score_trace_id_created_at` ON `scores` (`trace_id`, `created_at`);
-- create index "score_session_id_created_at" to table: "scores"
CREATE INDEX `score_session_id_created_at` ON `scores` (`session_id`, `created_at`);
-- create index "score_dataset_id_created_at" to table: "scores"
CREATE INDEX `score_dataset_id_created_at` ON `scores` (`dataset_id`, `created_at`);
-- create index "score_eval_run_id_created_at" to table: "scores"
CREATE INDEX `score_eval_run_id_created_at` ON `scores` (`eval_run_id`, `created_at`);
-- create "logs" table
CREATE TABLE `logs` (`path` text NOT NULL, `trace_id` text NOT NULL, `mod_time_ns` integer NOT NULL, `file_size` integer NOT NULL, `version` text NOT NULL, `request_id` text NOT NULL DEFAULT (''), `recorded_at` datetime NOT NULL, `model` text NOT NULL DEFAULT (''), `provider` text NOT NULL DEFAULT (''), `operation` text NOT NULL DEFAULT (''), `endpoint` text NOT NULL DEFAULT (''), `url` text NOT NULL DEFAULT (''), `method` text NOT NULL DEFAULT (''), `status_code` integer NOT NULL DEFAULT (0), `duration_ms` integer NOT NULL DEFAULT (0), `ttft_ms` integer NOT NULL DEFAULT (0), `client_ip` text NOT NULL DEFAULT (''), `content_length` integer NOT NULL DEFAULT (0), `error_text` text NOT NULL DEFAULT (''), `prompt_tokens` integer NOT NULL DEFAULT (0), `completion_tokens` integer NOT NULL DEFAULT (0), `total_tokens` integer NOT NULL DEFAULT (0), `cached_tokens` integer NOT NULL DEFAULT (0), `req_header_len` integer NOT NULL DEFAULT (0), `req_body_len` integer NOT NULL DEFAULT (0), `res_header_len` integer NOT NULL DEFAULT (0), `res_body_len` integer NOT NULL DEFAULT (0), `is_stream` bool NOT NULL DEFAULT (false), `session_id` text NOT NULL DEFAULT (''), `session_source` text NOT NULL DEFAULT (''), `window_id` text NOT NULL DEFAULT (''), `client_request_id` text NOT NULL DEFAULT (''), `selected_upstream_id` text NOT NULL DEFAULT (''), `selected_upstream_base_url` text NOT NULL DEFAULT (''), `selected_upstream_provider_preset` text NOT NULL DEFAULT (''), `routing_policy` text NOT NULL DEFAULT (''), `routing_score` real NOT NULL DEFAULT (0), `routing_candidate_count` integer NOT NULL DEFAULT (0), `routing_failure_reason` text NOT NULL DEFAULT (''), PRIMARY KEY (`path`));
-- set sequence for "logs" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("logs", 30064771072);
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
-- create "upstream_models" table
CREATE TABLE `upstream_models` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `upstream_id` text NOT NULL, `model` text NOT NULL, `source` text NOT NULL DEFAULT (''), `seen_at` datetime NOT NULL);
-- set sequence for "upstream_models" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("upstream_models", 34359738368);
-- create index "upstreammodel_upstream_id_model" to table: "upstream_models"
CREATE UNIQUE INDEX `upstreammodel_upstream_id_model` ON `upstream_models` (`upstream_id`, `model`);
-- create index "upstreammodel_model" to table: "upstream_models"
CREATE INDEX `upstreammodel_model` ON `upstream_models` (`model`);
-- create "upstream_targets" table
CREATE TABLE `upstream_targets` (`id` text NOT NULL, `base_url` text NOT NULL DEFAULT (''), `provider_preset` text NOT NULL DEFAULT (''), `protocol_family` text NOT NULL DEFAULT (''), `routing_profile` text NOT NULL DEFAULT (''), `enabled` bool NOT NULL DEFAULT (true), `priority` integer NOT NULL DEFAULT (0), `weight` real NOT NULL DEFAULT (0), `capacity_hint` real NOT NULL DEFAULT (0), `last_refresh_at` datetime NULL, `last_refresh_status` text NOT NULL DEFAULT (''), `last_refresh_error` text NOT NULL DEFAULT (''), PRIMARY KEY (`id`));
-- set sequence for "upstream_targets" table
INSERT INTO sqlite_sequence (name, seq) VALUES ("upstream_targets", 38654705664);
