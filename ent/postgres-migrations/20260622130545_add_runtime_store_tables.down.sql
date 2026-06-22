-- reverse: create index "traceobservation_status_updated_at" to table: "trace_observations"
DROP INDEX "traceobservation_status_updated_at";
-- reverse: create "trace_observations" table
DROP TABLE "trace_observations";
-- reverse: create index "tracefinding_trace_id_severity_category" to table: "trace_findings"
DROP INDEX "tracefinding_trace_id_severity_category";
-- reverse: create index "tracefinding_trace_id_finding_id" to table: "trace_findings"
DROP INDEX "tracefinding_trace_id_finding_id";
-- reverse: create "trace_findings" table
DROP TABLE "trace_findings";
-- reverse: create index "systemevent_trace_id_last_seen_at" to table: "system_events"
DROP INDEX "systemevent_trace_id_last_seen_at";
-- reverse: create index "systemevent_status_last_seen_at" to table: "system_events"
DROP INDEX "systemevent_status_last_seen_at";
-- reverse: create index "systemevent_source_category_last_seen_at" to table: "system_events"
DROP INDEX "systemevent_source_category_last_seen_at";
-- reverse: create index "system_events_fingerprint_key" to table: "system_events"
DROP INDEX "system_events_fingerprint_key";
-- reverse: create "system_events" table
DROP TABLE "system_events";
-- reverse: create index "semanticnode_trace_id_node_id" to table: "semantic_nodes"
DROP INDEX "semanticnode_trace_id_node_id";
-- reverse: create index "semanticnode_trace_id_depth_node_index" to table: "semantic_nodes"
DROP INDEX "semanticnode_trace_id_depth_node_index";
-- reverse: create "semantic_nodes" table
DROP TABLE "semantic_nodes";
-- reverse: create index "parserversion_parser_version" to table: "parser_versions"
DROP INDEX "parserversion_parser_version";
-- reverse: create "parser_versions" table
DROP TABLE "parser_versions";
-- reverse: create index "parsejob_status_updated_at" to table: "parse_jobs"
DROP INDEX "parsejob_status_updated_at";
-- reverse: create "parse_jobs" table
DROP TABLE "parse_jobs";
-- reverse: create index "analysisrun_trace_id_kind_created_at" to table: "analysis_runs"
DROP INDEX "analysisrun_trace_id_kind_created_at";
-- reverse: create index "analysisrun_session_id_kind_created_at" to table: "analysis_runs"
DROP INDEX "analysisrun_session_id_kind_created_at";
-- reverse: create "analysis_runs" table
DROP TABLE "analysis_runs";
-- reverse: create index "analysisjob_target_type_target_id_created_at" to table: "analysis_jobs"
DROP INDEX "analysisjob_target_type_target_id_created_at";
-- reverse: create index "analysisjob_status_updated_at" to table: "analysis_jobs"
DROP INDEX "analysisjob_status_updated_at";
-- reverse: create "analysis_jobs" table
DROP TABLE "analysis_jobs";
