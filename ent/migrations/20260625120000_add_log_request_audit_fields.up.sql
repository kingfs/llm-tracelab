ALTER TABLE `logs` ADD COLUMN `request_audit_id` text NOT NULL DEFAULT '';
ALTER TABLE `logs` ADD COLUMN `response_id` text NOT NULL DEFAULT '';
CREATE INDEX `tracelog_request_audit_id_recorded_at` ON `logs` (`request_audit_id`, `recorded_at`);
