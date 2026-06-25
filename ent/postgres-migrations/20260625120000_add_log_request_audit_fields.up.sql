ALTER TABLE "logs"
	ADD COLUMN "request_audit_id" character varying NOT NULL DEFAULT '',
	ADD COLUMN "response_id" character varying NOT NULL DEFAULT '';

CREATE INDEX "tracelog_request_audit_id_recorded_at" ON "logs" ("request_audit_id", "recorded_at");
