ALTER TABLE "channel_models"
	DROP COLUMN IF EXISTS "profile_adoption_status",
	DROP COLUMN IF EXISTS "profile_source",
	DROP COLUMN IF EXISTS "upstream_model",
	DROP COLUMN IF EXISTS "compact_history_item_threshold",
	DROP COLUMN IF EXISTS "max_output_tokens";
