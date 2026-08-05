DROP INDEX "tracelog_request_audit_id_recorded_at";

ALTER TABLE "logs"
	DROP COLUMN "response_id",
	DROP COLUMN "request_audit_id";
