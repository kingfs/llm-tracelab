ALTER TABLE `logs` ADD COLUMN `exchange_id` text NOT NULL DEFAULT '';
ALTER TABLE `logs` ADD COLUMN `exchange_kind` text NOT NULL DEFAULT '';
ALTER TABLE `logs` ADD COLUMN `exchange_role` text NOT NULL DEFAULT '';
ALTER TABLE `logs` ADD COLUMN `parent_exchange_id` text NOT NULL DEFAULT '';
ALTER TABLE `logs` ADD COLUMN `sequence_index` integer NOT NULL DEFAULT 0;
CREATE INDEX `tracelog_exchange_kind_recorded_at` ON `logs` (`exchange_kind`, `recorded_at`);
CREATE INDEX `tracelog_parent_exchange_id` ON `logs` (`parent_exchange_id`);
