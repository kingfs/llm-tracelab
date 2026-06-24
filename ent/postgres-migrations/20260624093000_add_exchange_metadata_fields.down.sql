DROP INDEX "upstreamexchange_parent_exchange_id";
DROP INDEX "upstreamexchange_exchange_id";
ALTER TABLE "upstream_exchanges"
	DROP COLUMN "sequence_index",
	DROP COLUMN "parent_exchange_id",
	DROP COLUMN "exchange_role",
	DROP COLUMN "exchange_kind",
	DROP COLUMN "exchange_id";
