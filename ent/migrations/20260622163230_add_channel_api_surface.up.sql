ALTER TABLE `channel_configs` ADD COLUMN `api_type` text NOT NULL DEFAULT '';
ALTER TABLE `channel_configs` ADD COLUMN `mode` text NOT NULL DEFAULT '';
ALTER TABLE `channel_configs` ADD COLUMN `capabilities_json` text NOT NULL DEFAULT '{}';
