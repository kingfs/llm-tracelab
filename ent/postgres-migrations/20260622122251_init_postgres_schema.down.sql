-- reverse: create index "apitoken_prefix" to table: "api_tokens"
DROP INDEX "apitoken_prefix";
-- reverse: create index "apitoken_enabled" to table: "api_tokens"
DROP INDEX "apitoken_enabled";
-- reverse: create index "api_tokens_token_hash_key" to table: "api_tokens"
DROP INDEX "api_tokens_token_hash_key";
-- reverse: create "api_tokens" table
DROP TABLE "api_tokens";
-- reverse: create "upstream_targets" table
DROP TABLE "upstream_targets";
-- reverse: create index "upstreammodel_upstream_id_model" to table: "upstream_models"
DROP INDEX "upstreammodel_upstream_id_model";
-- reverse: create index "upstreammodel_model" to table: "upstream_models"
DROP INDEX "upstreammodel_model";
-- reverse: create "upstream_models" table
DROP TABLE "upstream_models";
-- reverse: create index "upstreamexchange_upstream_id_started_at" to table: "upstream_exchanges"
DROP INDEX "upstreamexchange_upstream_id_started_at";
-- reverse: create index "upstreamexchange_trace_id" to table: "upstream_exchanges"
DROP INDEX "upstreamexchange_trace_id";
-- reverse: create index "upstreamexchange_status_code_started_at" to table: "upstream_exchanges"
DROP INDEX "upstreamexchange_status_code_started_at";
-- reverse: create index "upstreamexchange_response_id_started_at" to table: "upstream_exchanges"
DROP INDEX "upstreamexchange_response_id_started_at";
-- reverse: create index "upstreamexchange_request_audit_id_started_at" to table: "upstream_exchanges"
DROP INDEX "upstreamexchange_request_audit_id_started_at";
-- reverse: create "upstream_exchanges" table
DROP TABLE "upstream_exchanges";
-- reverse: create index "tracelog_session_id_recorded_at" to table: "logs"
DROP INDEX "tracelog_session_id_recorded_at";
-- reverse: create index "tracelog_request_id" to table: "logs"
DROP INDEX "tracelog_request_id";
-- reverse: create index "tracelog_recorded_at" to table: "logs"
DROP INDEX "tracelog_recorded_at";
-- reverse: create index "tracelog_model_recorded_at" to table: "logs"
DROP INDEX "tracelog_model_recorded_at";
-- reverse: create index "logs_trace_id_key" to table: "logs"
DROP INDEX "logs_trace_id_key";
-- reverse: create "logs" table
DROP TABLE "logs";
-- reverse: create index "score_trace_id_created_at" to table: "scores"
DROP INDEX "score_trace_id_created_at";
-- reverse: create index "score_session_id_created_at" to table: "scores"
DROP INDEX "score_session_id_created_at";
-- reverse: create index "score_eval_run_id_created_at" to table: "scores"
DROP INDEX "score_eval_run_id_created_at";
-- reverse: create index "score_dataset_id_created_at" to table: "scores"
DROP INDEX "score_dataset_id_created_at";
-- reverse: create "scores" table
DROP TABLE "scores";
-- reverse: create index "responseitem_response_id_kind" to table: "response_items"
DROP INDEX "responseitem_response_id_kind";
-- reverse: create index "responseitem_conversation_id_created_at" to table: "response_items"
DROP INDEX "responseitem_conversation_id_created_at";
-- reverse: create "response_items" table
DROP TABLE "response_items";
-- reverse: create index "response_status_created_at" to table: "responses"
DROP INDEX "response_status_created_at";
-- reverse: create index "response_previous_response_id" to table: "responses"
DROP INDEX "response_previous_response_id";
-- reverse: create index "response_conversation_id_created_at" to table: "responses"
DROP INDEX "response_conversation_id_created_at";
-- reverse: create "responses" table
DROP TABLE "responses";
-- reverse: create index "channelproberun_status_started_at" to table: "channel_probe_runs"
DROP INDEX "channelproberun_status_started_at";
-- reverse: create index "channelproberun_channel_id_started_at" to table: "channel_probe_runs"
DROP INDEX "channelproberun_channel_id_started_at";
-- reverse: create "channel_probe_runs" table
DROP TABLE "channel_probe_runs";
-- reverse: create index "users_username_key" to table: "users"
DROP INDEX "users_username_key";
-- reverse: create "users" table
DROP TABLE "users";
-- reverse: create index "channelconfig_provider_preset" to table: "channel_configs"
DROP INDEX "channelconfig_provider_preset";
-- reverse: create index "channelconfig_enabled_priority" to table: "channel_configs"
DROP INDEX "channelconfig_enabled_priority";
-- reverse: create "channel_configs" table
DROP TABLE "channel_configs";
-- reverse: create index "executionevent_response_id_occurred_at" to table: "execution_events"
DROP INDEX "executionevent_response_id_occurred_at";
-- reverse: create index "executionevent_request_audit_id_occurred_at" to table: "execution_events"
DROP INDEX "executionevent_request_audit_id_occurred_at";
-- reverse: create index "executionevent_phase_status_occurred_at" to table: "execution_events"
DROP INDEX "executionevent_phase_status_occurred_at";
-- reverse: create index "executionevent_event_type_occurred_at" to table: "execution_events"
DROP INDEX "executionevent_event_type_occurred_at";
-- reverse: create index "executionevent_conversation_id_occurred_at" to table: "execution_events"
DROP INDEX "executionevent_conversation_id_occurred_at";
-- reverse: create "execution_events" table
DROP TABLE "execution_events";
-- reverse: create index "evalrun_created_at" to table: "eval_runs"
DROP INDEX "evalrun_created_at";
-- reverse: create "eval_runs" table
DROP TABLE "eval_runs";
-- reverse: create index "datasetexample_dataset_id_trace_id" to table: "dataset_examples"
DROP INDEX "datasetexample_dataset_id_trace_id";
-- reverse: create index "datasetexample_dataset_id_position" to table: "dataset_examples"
DROP INDEX "datasetexample_dataset_id_position";
-- reverse: create "dataset_examples" table
DROP TABLE "dataset_examples";
-- reverse: create index "dataset_updated_at" to table: "datasets"
DROP INDEX "dataset_updated_at";
-- reverse: create "datasets" table
DROP TABLE "datasets";
-- reverse: create "model_catalog" table
DROP TABLE "model_catalog";
-- reverse: create index "channelmodel_model" to table: "channel_models"
DROP INDEX "channelmodel_model";
-- reverse: create index "channelmodel_channel_id_model" to table: "channel_models"
DROP INDEX "channelmodel_channel_id_model";
-- reverse: create index "channelmodel_channel_id_enabled" to table: "channel_models"
DROP INDEX "channelmodel_channel_id_enabled";
-- reverse: create "channel_models" table
DROP TABLE "channel_models";
-- reverse: create index "requestaudit_status_created_at" to table: "request_audits"
DROP INDEX "requestaudit_status_created_at";
-- reverse: create index "requestaudit_response_id_created_at" to table: "request_audits"
DROP INDEX "requestaudit_response_id_created_at";
-- reverse: create index "requestaudit_conversation_id_created_at" to table: "request_audits"
DROP INDEX "requestaudit_conversation_id_created_at";
-- reverse: create index "requestaudit_client_request_id" to table: "request_audits"
DROP INDEX "requestaudit_client_request_id";
-- reverse: create "request_audits" table
DROP TABLE "request_audits";
-- reverse: create index "experimentrun_created_at_id" to table: "experiment_runs"
DROP INDEX "experimentrun_created_at_id";
-- reverse: create "experiment_runs" table
DROP TABLE "experiment_runs";
