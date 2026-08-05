ALTER TABLE "logs"
	ADD COLUMN "exchange_id" character varying NOT NULL DEFAULT '',
	ADD COLUMN "exchange_kind" character varying NOT NULL DEFAULT '',
	ADD COLUMN "exchange_role" character varying NOT NULL DEFAULT '',
	ADD COLUMN "parent_exchange_id" character varying NOT NULL DEFAULT '',
	ADD COLUMN "sequence_index" bigint NOT NULL DEFAULT 0;
CREATE INDEX "tracelog_exchange_kind_recorded_at" ON "logs" ("exchange_kind", "recorded_at");
CREATE INDEX "tracelog_parent_exchange_id" ON "logs" ("parent_exchange_id");
