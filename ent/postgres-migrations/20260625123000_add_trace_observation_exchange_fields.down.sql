DROP INDEX "traceobservation_response_id_updated_at";
DROP INDEX "traceobservation_request_audit_id_updated_at";

ALTER TABLE "trace_observations"
	DROP COLUMN "response_id",
	DROP COLUMN "request_audit_id",
	DROP COLUMN "sequence_index",
	DROP COLUMN "parent_exchange_id",
	DROP COLUMN "exchange_role",
	DROP COLUMN "exchange_kind";
