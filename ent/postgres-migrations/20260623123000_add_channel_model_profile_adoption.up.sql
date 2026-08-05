ALTER TABLE "channel_models"
	ADD COLUMN IF NOT EXISTS "max_output_tokens" bigint NULL,
	ADD COLUMN IF NOT EXISTS "compact_history_item_threshold" bigint NULL,
	ADD COLUMN IF NOT EXISTS "upstream_model" character varying NOT NULL DEFAULT '',
	ADD COLUMN IF NOT EXISTS "profile_source" character varying NOT NULL DEFAULT '',
	ADD COLUMN IF NOT EXISTS "profile_adoption_status" character varying NOT NULL DEFAULT '';
