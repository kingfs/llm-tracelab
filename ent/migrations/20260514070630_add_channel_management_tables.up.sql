CREATE TABLE IF NOT EXISTS `channel_configs` (
  `id` text NOT NULL,
  `name` text NOT NULL,
  `description` text NOT NULL DEFAULT (''),
  `base_url` text NOT NULL,
  `provider_preset` text NOT NULL DEFAULT (''),
  `protocol_family` text NOT NULL DEFAULT (''),
  `routing_profile` text NOT NULL DEFAULT (''),
  `api_version` text NOT NULL DEFAULT (''),
  `deployment` text NOT NULL DEFAULT (''),
  `project` text NOT NULL DEFAULT (''),
  `location` text NOT NULL DEFAULT (''),
  `model_resource` text NOT NULL DEFAULT (''),
  `api_key_ciphertext` blob NULL,
  `api_key_hint` text NOT NULL DEFAULT (''),
  `headers_json` text NOT NULL DEFAULT ('{}'),
  `enabled` bool NOT NULL DEFAULT (true),
  `priority` integer NOT NULL DEFAULT (0),
  `weight` real NOT NULL DEFAULT (1),
  `capacity_hint` real NOT NULL DEFAULT (1),
  `model_discovery` text NOT NULL DEFAULT ('list_models'),
  `allow_unknown_models` bool NOT NULL DEFAULT (false),
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  `last_probe_at` datetime NULL,
  `last_probe_status` text NOT NULL DEFAULT (''),
  `last_probe_error` text NOT NULL DEFAULT (''),
  PRIMARY KEY (`id`)
);

CREATE INDEX IF NOT EXISTS `channelconfig_enabled_priority` ON `channel_configs` (`enabled`, `priority`);
CREATE INDEX IF NOT EXISTS `channelconfig_provider_preset` ON `channel_configs` (`provider_preset`);

CREATE TABLE IF NOT EXISTS `channel_models` (
  `id` integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  `channel_id` text NOT NULL,
  `model` text NOT NULL,
  `display_name` text NOT NULL DEFAULT (''),
  `source` text NOT NULL DEFAULT (''),
  `enabled` bool NOT NULL DEFAULT (true),
  `supports_responses` integer NULL,
  `supports_chat_completions` integer NULL,
  `supports_embeddings` integer NULL,
  `context_window` integer NULL,
  `input_modalities_json` text NOT NULL DEFAULT ('[]'),
  `output_modalities_json` text NOT NULL DEFAULT ('[]'),
  `raw_model_json` text NOT NULL DEFAULT ('{}'),
  `first_seen_at` datetime NOT NULL,
  `last_seen_at` datetime NOT NULL,
  `last_probe_at` datetime NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS `channelmodel_channel_id_model` ON `channel_models` (`channel_id`, `model`);
CREATE INDEX IF NOT EXISTS `channelmodel_model` ON `channel_models` (`model`);
CREATE INDEX IF NOT EXISTS `channelmodel_channel_id_enabled` ON `channel_models` (`channel_id`, `enabled`);

CREATE TABLE IF NOT EXISTS `channel_probe_runs` (
  `id` text NOT NULL,
  `channel_id` text NOT NULL,
  `status` text NOT NULL,
  `started_at` datetime NOT NULL,
  `completed_at` datetime NULL,
  `duration_ms` integer NOT NULL DEFAULT (0),
  `discovered_count` integer NOT NULL DEFAULT (0),
  `enabled_count` integer NOT NULL DEFAULT (0),
  `endpoint` text NOT NULL DEFAULT (''),
  `status_code` integer NOT NULL DEFAULT (0),
  `error_text` text NOT NULL DEFAULT (''),
  `request_meta_json` text NOT NULL DEFAULT ('{}'),
  `response_sample_json` text NOT NULL DEFAULT ('{}'),
  PRIMARY KEY (`id`)
);

CREATE INDEX IF NOT EXISTS `channelproberun_channel_id_started_at` ON `channel_probe_runs` (`channel_id`, `started_at`);
CREATE INDEX IF NOT EXISTS `channelproberun_status_started_at` ON `channel_probe_runs` (`status`, `started_at`);

CREATE TABLE IF NOT EXISTS `model_catalog` (
  `model` text NOT NULL,
  `display_name` text NOT NULL DEFAULT (''),
  `family` text NOT NULL DEFAULT (''),
  `vendor` text NOT NULL DEFAULT (''),
  `description` text NOT NULL DEFAULT (''),
  `tags_json` text NOT NULL DEFAULT ('[]'),
  `first_seen_at` datetime NOT NULL,
  `last_seen_at` datetime NOT NULL,
  `last_used_at` datetime NULL,
  PRIMARY KEY (`model`)
);
