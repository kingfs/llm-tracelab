CREATE TABLE IF NOT EXISTS `upstream_exchanges` (
	`id` text NOT NULL,
	`response_id` text NULL,
	`request_audit_id` text NULL,
	`trace_id` text NULL,
	`cassette_path` text NULL,
	`upstream_id` text NULL,
	`route_target` text NULL,
	`model` text NULL,
	`endpoint` text NULL,
	`status_code` integer NULL,
	`started_at` datetime NULL,
	`completed_at` datetime NULL,
	`error_text` text NULL,
	PRIMARY KEY (`id`)
);
ALTER TABLE `upstream_exchanges` ADD COLUMN `exchange_id` text NULL;
ALTER TABLE `upstream_exchanges` ADD COLUMN `exchange_kind` text NULL;
ALTER TABLE `upstream_exchanges` ADD COLUMN `exchange_role` text NULL;
ALTER TABLE `upstream_exchanges` ADD COLUMN `parent_exchange_id` text NULL;
ALTER TABLE `upstream_exchanges` ADD COLUMN `sequence_index` integer NULL;
CREATE INDEX IF NOT EXISTS `upstreamexchange_request_audit_id_started_at` ON `upstream_exchanges` (`request_audit_id`, `started_at`);
CREATE INDEX IF NOT EXISTS `upstreamexchange_response_id_started_at` ON `upstream_exchanges` (`response_id`, `started_at`);
CREATE INDEX IF NOT EXISTS `upstreamexchange_status_code_started_at` ON `upstream_exchanges` (`status_code`, `started_at`);
CREATE INDEX IF NOT EXISTS `upstreamexchange_trace_id` ON `upstream_exchanges` (`trace_id`);
CREATE INDEX IF NOT EXISTS `upstreamexchange_exchange_id` ON `upstream_exchanges` (`exchange_id`);
CREATE INDEX IF NOT EXISTS `upstreamexchange_parent_exchange_id` ON `upstream_exchanges` (`parent_exchange_id`);
CREATE INDEX IF NOT EXISTS `upstreamexchange_upstream_id_started_at` ON `upstream_exchanges` (`upstream_id`, `started_at`);
