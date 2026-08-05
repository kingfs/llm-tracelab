DROP INDEX `upstreamexchange_parent_exchange_id`;
DROP INDEX `upstreamexchange_exchange_id`;
ALTER TABLE `upstream_exchanges` DROP COLUMN `sequence_index`;
ALTER TABLE `upstream_exchanges` DROP COLUMN `parent_exchange_id`;
ALTER TABLE `upstream_exchanges` DROP COLUMN `exchange_role`;
ALTER TABLE `upstream_exchanges` DROP COLUMN `exchange_kind`;
ALTER TABLE `upstream_exchanges` DROP COLUMN `exchange_id`;
