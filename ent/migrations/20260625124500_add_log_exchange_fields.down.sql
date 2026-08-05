DROP INDEX `tracelog_parent_exchange_id`;
DROP INDEX `tracelog_exchange_kind_recorded_at`;
ALTER TABLE `logs` DROP COLUMN `sequence_index`;
ALTER TABLE `logs` DROP COLUMN `parent_exchange_id`;
ALTER TABLE `logs` DROP COLUMN `exchange_role`;
ALTER TABLE `logs` DROP COLUMN `exchange_kind`;
ALTER TABLE `logs` DROP COLUMN `exchange_id`;
