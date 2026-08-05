DROP INDEX "tracelog_parent_exchange_id";
DROP INDEX "tracelog_exchange_kind_recorded_at";
ALTER TABLE "logs"
	DROP COLUMN "sequence_index",
	DROP COLUMN "parent_exchange_id",
	DROP COLUMN "exchange_role",
	DROP COLUMN "exchange_kind",
	DROP COLUMN "exchange_id";
