ALTER TABLE "trace_observations"
	ADD COLUMN "exchange_kind" character varying NOT NULL DEFAULT '',
	ADD COLUMN "exchange_role" character varying NOT NULL DEFAULT '',
	ADD COLUMN "parent_exchange_id" character varying NOT NULL DEFAULT '',
	ADD COLUMN "sequence_index" bigint NOT NULL DEFAULT 0,
	ADD COLUMN "request_audit_id" character varying NOT NULL DEFAULT '',
	ADD COLUMN "response_id" character varying NOT NULL DEFAULT '';

CREATE INDEX "traceobservation_request_audit_id_updated_at" ON "trace_observations" ("request_audit_id", "updated_at");
CREATE INDEX "traceobservation_response_id_updated_at" ON "trace_observations" ("response_id", "updated_at");
