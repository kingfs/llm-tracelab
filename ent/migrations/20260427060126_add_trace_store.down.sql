-- reverse: set sequence for "upstream_targets" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "upstream_targets";
-- reverse: create "upstream_targets" table
DROP TABLE `upstream_targets`;
-- reverse: create index "upstreammodel_model" to table: "upstream_models"
DROP INDEX `upstreammodel_model`;
-- reverse: create index "upstreammodel_upstream_id_model" to table: "upstream_models"
DROP INDEX `upstreammodel_upstream_id_model`;
-- reverse: set sequence for "upstream_models" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "upstream_models";
-- reverse: create "upstream_models" table
DROP TABLE `upstream_models`;
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
-- reverse: set sequence for "logs" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "logs";
-- reverse: create "logs" table
DROP TABLE `logs`;
-- reverse: create index "score_eval_run_id_created_at" to table: "scores"
DROP INDEX `score_eval_run_id_created_at`;
-- reverse: create index "score_dataset_id_created_at" to table: "scores"
DROP INDEX `score_dataset_id_created_at`;
-- reverse: create index "score_session_id_created_at" to table: "scores"
DROP INDEX `score_session_id_created_at`;
-- reverse: create index "score_trace_id_created_at" to table: "scores"
DROP INDEX `score_trace_id_created_at`;
-- reverse: set sequence for "scores" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "scores";
-- reverse: create "scores" table
DROP TABLE `scores`;
-- reverse: create index "experimentrun_created_at_id" to table: "experiment_runs"
DROP INDEX `experimentrun_created_at_id`;
-- reverse: set sequence for "experiment_runs" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "experiment_runs";
-- reverse: create "experiment_runs" table
DROP TABLE `experiment_runs`;
-- reverse: create index "evalrun_created_at" to table: "eval_runs"
DROP INDEX `evalrun_created_at`;
-- reverse: set sequence for "eval_runs" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "eval_runs";
-- reverse: create "eval_runs" table
DROP TABLE `eval_runs`;
-- reverse: create index "datasetexample_dataset_id_position" to table: "dataset_examples"
DROP INDEX `datasetexample_dataset_id_position`;
-- reverse: create index "datasetexample_dataset_id_trace_id" to table: "dataset_examples"
DROP INDEX `datasetexample_dataset_id_trace_id`;
-- reverse: set sequence for "dataset_examples" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "dataset_examples";
-- reverse: create "dataset_examples" table
DROP TABLE `dataset_examples`;
-- reverse: create index "dataset_updated_at" to table: "datasets"
DROP INDEX `dataset_updated_at`;
-- reverse: set sequence for "datasets" table
UPDATE sqlite_sequence SET seq = 0 WHERE name = "datasets";
-- reverse: create "datasets" table
DROP TABLE `datasets`;
