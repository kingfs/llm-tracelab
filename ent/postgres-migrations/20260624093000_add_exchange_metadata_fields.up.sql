ALTER TABLE "upstream_exchanges"
	ADD COLUMN "exchange_id" character varying NULL,
	ADD COLUMN "exchange_kind" character varying NULL,
	ADD COLUMN "exchange_role" character varying NULL,
	ADD COLUMN "parent_exchange_id" character varying NULL,
	ADD COLUMN "sequence_index" bigint NULL;
CREATE INDEX "upstreamexchange_exchange_id" ON "upstream_exchanges" ("exchange_id");
CREATE INDEX "upstreamexchange_parent_exchange_id" ON "upstream_exchanges" ("parent_exchange_id");
